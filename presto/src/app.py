# swkit panel for the Pimoroni Presto: boot, Wi-Fi, controller discovery,
# polling, ambient LEDs and screen dimming. Drawing and touch are in ui.py,
# device state handling in model.py.

try:
    import asyncio
except ImportError:
    import uasyncio as asyncio

try:
    import network
except ImportError:
    network = None

from compat import ticks_ms, ticks_diff, gc_collect
from settings import Settings
from api import Client
import mdns
import model as M

HOSTNAME = "swkit-panel"
RECONNECT_AFTER_MS = 30000     # unreachable this long -> look for the controller again


def _parse_url(url):
    """http://host[:port][/path] -> (host, port, path)."""
    u = url.strip()
    if "://" in u:
        u = u.split("://", 1)[1]
    hostport, _, path = u.partition("/")
    host, _, port = hostport.partition(":")
    return host, int(port or 80), "/" + (path.strip("/") or "control")


class App:
    VERSION = "1.0.0"

    def __init__(self):
        # The hostname must be set before Presto() brings Wi-Fi up; the
        # panel then answers to swkit-panel.local.
        if network is not None:
            try:
                network.hostname(HOSTNAME)
            except (AttributeError, ValueError, OSError):
                pass
        from presto import Presto, Buzzer
        self.presto = Presto(full_res=True, ambient_light=False)
        try:
            self.buzzer = Buzzer(43)
        except Exception:  # noqa: BLE001 - optional nicety
            self.buzzer = None
        self.settings = Settings()
        self.client = None
        self.server = None          # dict describing the chosen controller
        self.info_name = None
        self.model = None
        self.secret_url = ""
        self.press_flash = 0
        self.dimmed = False
        self.picker_task = None

        import ui
        self.ui = ui.UI(self)
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

    def apply_settings(self):
        s = self.settings
        if not self.dimmed:
            self.presto.set_backlight(max(0.1, min(1.0, s["backlight"])))
        self.presto.auto_ambient_leds(s["ambient"] == "glow")
        if s["ambient"] == "off":
            self._leds(0, 0, 0)

    def click(self):
        if not self.settings["click"] or self.buzzer is None:
            return
        self.buzzer.set_tone(2400)
        asyncio.create_task(self._click_off())

    async def _click_off(self):
        await asyncio.sleep(0.012)
        self.buzzer.set_tone(-1)

    def wake(self):
        self.ui.dimmed = False
        if self.dimmed:
            self.dimmed = False
            self.presto.set_backlight(max(0.1, min(1.0, self.settings["backlight"])))

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
        asyncio.create_task(self.ui.loop())
        asyncio.create_task(self.idle_loop())
        asyncio.create_task(self.led_loop())
        await self.connect_wifi()
        asyncio.create_task(self.wifi_watch())
        await self.choose_server()

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
        """EzWiFi never reconnects by itself."""
        while True:
            await asyncio.sleep(10)
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
            asyncio.create_task(self.poll_loop())
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
        ui.scroll["picker"] = 0
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
            if s["dim_after_s"] and idle >= s["dim_after_s"] and not self.dimmed and self.ui.t is None:
                self.dimmed = True
                self.ui.dimmed = True
                self.presto.set_backlight(max(0.02, min(1.0, s["dim_level"])))
            if not self.dimmed:
                self.ui.dimmed = False
            if s["home_after_s"] and idle >= s["home_after_s"] and self.ui.mode == "main":
                self.ui.go_home()
            if idle == 600:
                self.settings.save()   # persists the last page
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
            if self.settings["ambient"] != "status":
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


def main():
    app = App()
    try:
        asyncio.run(app.run_forever())
    except KeyboardInterrupt:
        raise
    except Exception as e:  # noqa: BLE001 - last-resort crash screen
        crashed(app, e)


def crashed(app, e):
    """Shows what went wrong, then restarts: the panel is unattended."""
    import sys
    try:
        sys.print_exception(e)
    except AttributeError:
        import traceback
        traceback.print_exc()
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
