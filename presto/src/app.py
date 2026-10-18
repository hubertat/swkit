# swkit panel for the Pimoroni Presto: boot, Wi-Fi, controller discovery,
# polling, ambient LEDs, screen dimming and screen off. Drawing and touch are in ui.py,
# device state handling in model.py.

try:
    import asyncio
except ImportError:
    import uasyncio as asyncio

try:
    import network
except ImportError:
    network = None

try:
    from machine import Timer
except ImportError:
    Timer = None

from compat import ticks_ms, ticks_diff, gc_collect
from settings import Settings, mount_ramfs
from api import Client
import mdns
import model as M

HOSTNAME = "swkit-panel"
RECONNECT_AFTER_MS = 30000     # unreachable this long -> look for the controller again
COMMIT_IDLE_S = 600            # untouched this long with unsaved settings -> restart to commit them
IDLE_FILE = "swkit_idle"       # in the RAM disk: "dim" or "off" carries the idle state over a restart
# Tap sound: a short tone, timed by a hardware timer so the length doesn't
# depend on how long the redraw after the tap takes. Lower than the piezo's
# loudest (shrill) range around 2.5-4 kHz.
CLICK_HZ = 1200
CLICK_MS = 16


def _parse_url(url):
    """http://host[:port][/path] -> (host, port, path)."""
    u = url.strip()
    if "://" in u:
        u = u.split("://", 1)[1]
    hostport, _, path = u.partition("/")
    host, _, port = hostport.partition(":")
    return host, int(port or 80), "/" + (path.strip("/") or "control")


class App:
    VERSION = "1.2.0"

    def __init__(self):
        # Settings first: flash may only be written before Presto() starts
        # core1 (see settings.py), so pending changes are committed here.
        ram_dir = mount_ramfs()
        self.settings = Settings(ram_dir=ram_dir)
        self.idle_path = (ram_dir.rstrip("/") + "/" + IDLE_FILE) if ram_dir else None
        resume = _take(self.idle_path)
        # Without a RAM copy a restart would lose changes instead of saving them.
        self.can_commit = self.settings.commit() and not self.settings.memory_only
        # The hostname must be set before Presto() brings Wi-Fi up; the
        # panel then answers to swkit-panel.local.
        if network is not None:
            try:
                network.hostname(HOSTNAME)
            except (AttributeError, ValueError, OSError):
                pass
        from presto import Presto, Buzzer
        self.presto = Presto(full_res=True, ambient_light=False)
        # Restarted while idle (settings commit): come back the same way,
        # without lighting up a dark room.
        self.asleep = resume == "off"
        self.dimmed = resume in ("dim", "off")
        if self.dimmed:
            self.update_backlight()
        try:
            self.buzzer = Buzzer(43)
        except Exception:  # noqa: BLE001 - optional nicety
            self.buzzer = None
        self.click_timer = None
        self.click_stop = self._click_stop      # bound once: the timer calls it from an IRQ
        if Timer is not None and self.buzzer is not None:
            try:
                self.click_timer = Timer()
            except Exception:  # noqa: BLE001 - falls back to an asyncio stop
                pass
        self.client = None
        self.server = None          # dict describing the chosen controller
        self.info_name = None
        self.model = None
        self.secret_url = ""
        self.press_flash = 0
        self.picker_task = None
        self.wifi_pm = None         # power-management mode to restore on wake
        self.wifi_check_now = False

        import ui
        self.ui = ui.UI(self)
        self.ui.dimmed = self.dimmed
        self.ui.asleep = self.asleep
        self.apply_settings()

    # ---------- helpers used by the UI ----------

    def title(self):
        if self.info_name:
            return self.info_name
        if self.server and self.server.get("name"):
            return self.server["name"]
        return "swkit"

    def net_info(self):
        info = {"host": HOSTNAME + ".local"}
        try:
            wlan = network.WLAN(network.STA_IF)
            info["ssid"] = wlan.config("ssid")
            rssi = wlan.status("rssi")
            q = "Excellent" if rssi > -55 else "Good" if rssi > -67 else "Fair" if rssi > -75 else "Weak"
            info["signal"] = "%s (%d dBm)" % (q, rssi)
            info["ip"] = self.presto.wifi.ipv4()
        except Exception:  # noqa: BLE001 - informational only
            pass
        return info

    def update_backlight(self):
        s = self.settings
        if self.asleep:
            level = 0.0             # fully off: the PWM duty goes to 0
        elif self.dimmed:
            level = max(0.02, min(1.0, s["dim_level"]))
        else:
            level = max(0.1, min(1.0, s["backlight"]))
        self.presto.set_backlight(level)

    def apply_settings(self):
        s = self.settings
        self.update_backlight()
        self.presto.auto_ambient_leds(s["ambient"] == "glow" and not self.asleep)
        if s["ambient"] == "off" or self.asleep:
            self._leds(0, 0, 0)

    def change_setting(self, key, value, save=True):
        """Applies a setting now. Saving only reaches the PSRAM RAM disk;
        flash is written at the next boot (see settings.py)."""
        self.settings.set(key, value, save=False)
        if save:
            self.settings.save()
        self.apply_settings()

    def click(self):
        if not self.settings["click"] or self.buzzer is None:
            return
        self.buzzer.set_tone(CLICK_HZ)
        if self.click_timer is not None:
            # An asyncio sleep would end only when the UI loop yields, after
            # the redraw that follows the tap: the length varied with it.
            # hard=True stops it from the timer interrupt, even mid-render.
            self.click_timer.init(mode=Timer.ONE_SHOT, period=CLICK_MS, callback=self.click_stop, hard=True)
        else:
            asyncio.create_task(self._click_off())

    def _click_stop(self, _timer):
        # Runs in interrupt context: no allocation (set_tone(0) only sets the duty).
        self.buzzer.set_tone(0)

    async def _click_off(self):
        await asyncio.sleep(CLICK_MS / 1000)
        self.buzzer.set_tone(0)

    def sleep(self):
        """Screen off: backlight and LEDs off, polling paused, Wi-Fi in
        power-save mode. The touch controller keeps working, so a touch
        wakes the panel (and only wakes it, as when dimmed)."""
        if self.asleep:
            return
        self.asleep = self.dimmed = True
        self.ui.asleep = self.ui.dimmed = True
        self.update_backlight()
        self.presto.auto_ambient_leds(False)
        self._leds(0, 0, 0)
        self.wifi_powersave(True)

    def wake(self):
        self.ui.dimmed = False
        if self.asleep:
            self.asleep = False
            self.ui.asleep = False
            self.wifi_powersave(False)
            self.wifi_check_now = True
            if self.model is not None:
                self.model.resume()
            # Paint the current state while the backlight is still off, so
            # the screen doesn't light up showing what it was when it slept.
            self.ui.repaint_now()
            self.presto.auto_ambient_leds(self.settings["ambient"] == "glow")
        if self.dimmed:
            self.dimmed = False
            self.update_backlight()

    def wifi_powersave(self, on):
        """Lets the radio sleep between beacons while the screen is off. The
        connection stays up, so waking needs no reconnect."""
        if network is None:
            return
        try:
            wlan = network.WLAN(network.STA_IF)
            if on:
                if self.wifi_pm is None:
                    self.wifi_pm = wlan.config("pm")
                wlan.config(pm=network.WLAN.PM_POWERSAVE)
            elif self.wifi_pm is not None:
                wlan.config(pm=self.wifi_pm)
                self.wifi_pm = None
        except Exception as e:  # noqa: BLE001 - a saving, not a requirement
            print("wifi power save:", e)

    def notify(self, msg, level=None):
        self.ui.notify(msg, level)

    def font_missing(self):
        # Without the vector font nothing else can be drawn; use the
        # built-in bitmap font to say so.
        d = self.presto.display
        d.set_pen(d.create_pen(0, 0, 0))
        d.clear()
        d.set_pen(d.create_pen(255, 180, 60))
        d.set_font("bitmap8")
        d.text("swkit: font /swkit/swkit.af is missing. Re-run deploy.sh (see presto/README.md).", 20, 200, 440, 3)
        self.presto.update()
        print("swkit: /swkit/swkit.af missing")

    # ---------- startup ----------

    async def run_forever(self):
        await self.run()
        while True:
            await asyncio.sleep(3600)

    async def run(self):
        if not self.ui.g.font_ok:
            self.font_missing()
            return
        asyncio.get_event_loop().set_exception_handler(self._task_error)
        self.spawn("ui", self.ui.loop)
        self.spawn("idle", self.idle_loop)
        self.spawn("leds", self.led_loop)
        await self.connect_wifi()
        if self.asleep:
            self.wifi_powersave(True)   # restarted while the screen was off
        self.spawn("wifi", self.wifi_watch)
        await self.choose_server()

    def spawn(self, name, loop_fn):
        """Runs a long-lived loop and restarts it if it raises.

        An exception in an asyncio task ends only that task; for the UI loop
        that froze the screen while everything else kept running. Repeated
        failures restart the panel instead."""
        async def supervised():
            failures = []
            while True:
                try:
                    await loop_fn()
                    return
                except Exception as e:  # noqa: BLE001 - logged and retried
                    _log_exception(name, e)
                    now = ticks_ms()
                    failures = [t for t in failures if ticks_diff(now, t) < 60000] + [now]
                    if len(failures) >= 5:
                        crashed(self, e)
                    try:
                        self.notify("Internal error in %s: %s" % (name, e), "error")
                        self.ui.invalidate()
                    except Exception:  # noqa: BLE001
                        pass
                    await asyncio.sleep(0.5)
        return asyncio.create_task(supervised())

    def _task_error(self, loop, ctx):
        # One-shot tasks (command sends, scans): log, tell the user, carry on.
        e = ctx.get("exception")
        _log_exception("task", e)
        try:
            self.notify("Internal error: %s" % e, "error")
        except Exception:  # noqa: BLE001
            pass

    def restart(self, title):
        """Clean restart, e.g. to commit settings to flash at boot. An idle
        panel (dimmed or off) comes back up idle."""
        print("restart:", title)
        self.save_idle_state()
        if not self.asleep:
            try:
                self.ui.set_boot("sync", title, "Restarting the panel...")
                self.ui.render((0, 0, 480, 480))
            except Exception:  # noqa: BLE001
                pass
        import machine
        machine.reset()

    def save_idle_state(self):
        """Tells the next boot (RAM disk, read once) to start dimmed or off."""
        if not self.idle_path:
            return
        try:
            with open(self.idle_path, "w") as f:
                f.write("off" if self.asleep else "dim" if self.dimmed else "")
        except OSError as e:
            print("idle state:", e)

    async def connect_wifi(self):
        ui = self.ui
        try:
            import secrets
            ssid = getattr(secrets, "WIFI_SSID", "")
            password = getattr(secrets, "WIFI_PASSWORD", "")
            self.secret_url = getattr(secrets, "SWKIT_URL", "") or ""
        except ImportError:
            ssid = password = ""
        if not ssid:
            ui.set_boot("wifi_off", "Wi-Fi not set up",
                        "Copy secrets.example.py to secrets.py on the Presto, fill in WIFI_SSID and WIFI_PASSWORD, then restart.")
            while True:
                await asyncio.sleep(3600)
        attempt = 0
        while True:
            attempt += 1
            ui.set_boot("wifi", "Connecting to Wi-Fi", "Joining \"%s\"%s" % (ssid, "" if attempt == 1 else " (attempt %d)" % attempt))
            ok = await self.presto.wifi.connect(ssid, password, timeout=20, retries=2)
            if ok:
                return
            code, why = self.presto.wifi.error()
            reason = {"WRONG_PASSWORD": "The password was rejected.", "NO_AP_FOUND": "The network wasn't found.",
                      "CONNECT_FAIL": "The access point refused the connection."}.get(why, "No answer from the network.")
            ui.set_boot("wifi_off", "Can't join \"%s\"" % ssid, reason + " Retrying in 15 seconds; check secrets.py if this persists.")
            await asyncio.sleep(15)

    async def wifi_watch(self):
        """EzWiFi never reconnects by itself. Checks every 10 s, not while
        the screen is off, and straight after waking."""
        last = ticks_ms()
        while True:
            await asyncio.sleep(1)
            if self.asleep or (not self.wifi_check_now and ticks_diff(ticks_ms(), last) < 10000):
                continue
            self.wifi_check_now = False
            last = ticks_ms()
            if not self.presto.wifi.isconnected():
                self.notify("Wi-Fi dropped, reconnecting...", "warn")
                try:
                    await self.presto.wifi.connect(timeout=20, retries=1)
                except Exception as e:  # noqa: BLE001
                    print("wifi reconnect:", e)

    async def choose_server(self):
        ui = self.ui
        if self.secret_url:
            host, port, path = _parse_url(self.secret_url)
            await self.connect_server({"name": "", "host": host, "port": port, "path": path, "fixed": True})
            return
        saved = self.settings["server"]
        if saved:
            ui.set_boot("dns", "Connecting", "Looking for %s..." % (saved.get("name") or saved.get("host")))
            if await self.try_server(saved):
                return
            # It may have moved to another address: look it up by name.
            found = await mdns.browse()
            for s in found:
                if s["name"] == saved.get("name"):
                    if await self.try_server(s):
                        return
            ui.set_boot("cloud_off", "Can't reach %s" % (saved.get("name") or saved.get("host")),
                        "It didn't answer at %s:%s and wasn't found on the network." % (saved.get("ip") or saved.get("host"), saved.get("port")),
                        (("Find others", self.start_picker), ("Retry", self.restart_choose)))
            return
        await self.discover_first()

    def restart_choose(self):
        asyncio.create_task(self.choose_server())

    async def discover_first(self):
        ui = self.ui
        ui.set_boot("search", "Looking for swkit", "Searching the local network for controllers...")
        found = await mdns.browse()
        if len(found) == 1 and await self.try_server(found[0]):
            return
        self.start_picker(found)

    async def try_server(self, s):
        host = s.get("ip") or s["host"]
        if host.endswith(".local"):
            ip = await mdns.resolve(host)
            if ip:
                host = ip
        client = Client(host, s["port"], s.get("path"))
        try:
            info = await client.info()
            await client.get("/api/devices", 5.0)
        except Exception as e:  # noqa: BLE001
            print("server %s unreachable: %s" % (client.label(), e))
            return False
        self.use_client(client, s, info)
        return True

    async def connect_server(self, s):
        ui = self.ui
        while not await self.try_server(s):
            ui.set_boot("cloud_off", "Can't reach the controller",
                        "No answer from %s:%d (SWKIT_URL in secrets.py). Retrying..." % (s["host"], s["port"]))
            await asyncio.sleep(10)

    def use_client(self, client, s, info):
        first = self.model is None
        self.client = client
        self.server = s
        self.info_name = (info or {}).get("name") or s.get("name") or None
        if not s.get("fixed"):
            saved = {"name": self.info_name or s.get("name") or "", "host": s.get("host"), "ip": client.host,
                     "port": client.port, "path": client.path}
            if saved != self.settings["server"]:
                self.settings.set("server", saved)
        if first:
            self.model = M.Model(client, self.notify)
            self.model.on_press = self.on_press
            self.spawn("poll", self.poll_loop)
        else:
            self.model.client = client
            self.model.devices = {}
            self.model.order = []
            self.model.intents = {}
            self.model.last_ok_at = 0
            self.model.last_err = None
            self.model.poll_now = True
            self.model.changed()
        self.ui.show_main()

    # ---------- picker ----------

    def start_picker(self, found=None):
        ui = self.ui
        ui.mode = "picker"
        ui.screens["picker"] = 0
        ui.picker = {"items": self._mark(found or []), "scanning": found is None, "note": ""}
        ui.invalidate()
        if found is None:
            self.rescan()

    def _mark(self, items):
        cur = self.client
        for s in items:
            s["current"] = bool(cur and (s.get("ip") == cur.host and s["port"] == cur.port))
        return items

    def rescan(self):
        if self.ui.picker.get("scanning") and self.picker_task:
            return
        self.ui.picker["scanning"] = True
        self.ui.invalidate()
        self.picker_task = asyncio.create_task(self._scan())

    async def _scan(self):
        try:
            found = await mdns.browse(3000)
        except Exception as e:  # noqa: BLE001
            print("browse:", e)
            found = []
        self.picker_task = None
        p = self.ui.picker
        p["items"] = self._mark(found)
        p["scanning"] = False
        p["note"] = ""
        self.ui.invalidate()

    def use_server(self, s):
        ui = self.ui
        ui.picker["note"] = "Connecting to %s..." % (s.get("name") or s["host"])
        ui.invalidate()

        async def go():
            if not await self.try_server(s):
                ui.picker["note"] = "%s didn't answer. Pick another or scan again." % (s.get("name") or s["host"])
                ui.invalidate()
        asyncio.create_task(go())

    def cancel_picker(self):
        if self.client is not None:
            self.ui.show_main()

    # ---------- polling ----------

    async def poll_loop(self):
        m = self.model
        last = 0
        unreachable_since = None
        while True:
            if self.asleep:
                # Nothing to show and no push channel yet: don't poll.
                unreachable_since = None
                await asyncio.sleep(0.2)
                continue
            now = ticks_ms()
            cadence = M.POLL_FAST_MS if m.has_active_intent() else M.POLL_MS
            if m.poll_now or ticks_diff(now, last) >= cadence:
                m.poll_now = False
                last = now
                ok = await m.poll()
                if ok:
                    unreachable_since = None
                elif unreachable_since is None:
                    unreachable_since = now
                elif ticks_diff(now, unreachable_since) > RECONNECT_AFTER_MS and not (self.server or {}).get("fixed"):
                    unreachable_since = now
                    await self.relocate()
            await asyncio.sleep(0.05)

    async def relocate(self):
        """The controller stopped answering: it may have a new address."""
        name = self.title()
        try:
            found = await mdns.browse()
        except Exception:  # noqa: BLE001
            return
        for s in found:
            if s["name"] == name and (s.get("ip") != self.client.host or s["port"] != self.client.port):
                if await self.try_server(s):
                    self.notify("%s moved to %s." % (name, self.client.label()))
                return

    def on_press(self, dev):
        self.press_flash = ticks_ms()

    # ---------- idle & LEDs ----------

    async def idle_loop(self):
        while True:
            await asyncio.sleep(1)
            s = self.settings
            idle = ticks_diff(ticks_ms(), self.ui.last_touch) // 1000
            # Off wins when its delay is the shorter one.
            if s["off_after_s"] and idle >= s["off_after_s"] and not self.asleep and self.ui.t is None:
                self.sleep()
            elif s["dim_after_s"] and idle >= s["dim_after_s"] and not self.dimmed and self.ui.t is None:
                self.dimmed = True
                self.ui.dimmed = True
                self.update_backlight()
            if not self.dimmed:
                self.ui.dimmed = False
            if s["home_after_s"] and idle >= s["home_after_s"] and self.ui.mode == "main":
                self.ui.go_home()
            if (idle >= COMMIT_IDLE_S and self.can_commit and self.settings.dirty() and self.ui.t is None
                    and not (self.model and self.model.has_active_intent())):
                self.restart("Saving settings")
            if self.ui.t is None:
                # Small, regular collections instead of a long one when the
                # 8 MB PSRAM heap eventually fills (polls allocate JSON).
                gc_collect()

    def _leds(self, r, g, b):
        for i in range(7):
            self.presto.set_led_rgb(i, r, g, b)

    async def led_loop(self):
        """'status' mode: blue flash on a button press, slow red pulse while
        the controller is unreachable, dark otherwise."""
        while True:
            await asyncio.sleep(0.05)
            if self.settings["ambient"] != "status" or self.asleep:
                continue
            now = ticks_ms()
            m = self.model
            r = g = b = 0
            since = ticks_diff(now, self.press_flash) if self.press_flash else 99999
            if since < 1200:
                k = 1 - since / 1200
                r, g, b = int(20 * k), int(70 * k), int(255 * k)
            elif m is not None and m.freshness()[0] == "stale":
                phase = (now % 3000) / 3000
                k = phase * 2 if phase < 0.5 else (1 - phase) * 2
                r = int(10 + 50 * k)
            if self.dimmed:
                r, g, b = r // 2, g // 2, b // 2
            self._leds(r, g, b)


def _take(path):
    """Reads and removes a one-shot file; None if there is none."""
    if not path:
        return None
    try:
        with open(path) as f:
            value = f.read().strip()
    except OSError:
        return None
    try:
        import os
        os.remove(path)
    except OSError:
        pass
    return value or None


def main():
    app = App()
    try:
        asyncio.run(app.run_forever())
    except KeyboardInterrupt:
        raise
    except Exception as e:  # noqa: BLE001 - last-resort crash screen
        crashed(app, e)


def _log_exception(where, e):
    import sys
    print("error in %s:" % where)
    try:
        sys.print_exception(e)
    except AttributeError:
        import traceback
        traceback.print_exception(type(e), e, e.__traceback__)


def crashed(app, e):
    """Shows what went wrong, then restarts: the panel is unattended."""
    _log_exception("app", e)
    try:
        ui = app.ui
        ui.set_boot("error", "Something went wrong", "%s: %s. Restarting in 20 seconds." % (type(e).__name__, e))
        ui.render((0, 0, 480, 480))
    except Exception:  # noqa: BLE001
        pass
    import time
    time.sleep(20)
    import machine
    machine.reset()
