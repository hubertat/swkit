# Device state model: a port of the web control UI's (server/control_static/
# control.js) intent logic, so the panel behaves the same as the phone page.
#
# Drivers report state asynchronously, so a read straight after a command
# often still returns the old value. Each command therefore creates an
# "intent" that steers what is displayed:
#
#   sending      request in flight; the tile shows the requested state, hatched
#   dragging     brightness slider moving; value not sent yet
#   waiting      server accepted it; still hatched until a poll that *started
#                after* the acknowledgement reports the requested value
#   unconfirmed  no matching report within CONFIRM_MS; the tile falls back to
#                the reported value with an amber marker
#
# Each device has two independent slots, "power" (on/off or scene step) and
# "bri" (brightness). Within a slot requests are strictly serialized; on/off
# and brightness keep only the latest unsent target, scene steps queue.
# A state the server could not read (state_error) stays "unknown" and is
# never shown as "off".

try:
    import asyncio
except ImportError:
    import uasyncio as asyncio

from compat import ticks_ms, ticks_diff, ticks_add

POLL_MS = 2000
POLL_FAST_MS = 500
POLL_TIMEOUT_S = 5.0
CMD_TIMEOUT_S = 8.0
CONFIRM_MS = 6000
UNCONFIRMED_HOLD_MS = 15000
STALE_MS = 6000
DEBOUNCE_MS = 160
RECENT_PRESS_MS = 3000
CONFIRM_FLASH_MS = 700

TYPE_NAME = {
    "light": "Light",
    "color_light": "Color light",
    "dimmable_light": "Dimmable light",
    "outlet": "Outlet",
    "scene": "Scene",
    "button": "Button",
}

# Pages in display order; each lists the device types it holds.
SECTIONS = (
    {"key": "lights", "title": "Lights", "types": ("light", "color_light", "dimmable_light"), "all_off": True},
    {"key": "outlets", "title": "Outlets", "types": ("outlet",), "all_off": True},
    {"key": "scenes", "title": "Scenes", "types": ("scene",), "all_off": False},
    {"key": "inputs", "title": "Buttons", "types": ("button",), "all_off": False},
)

PHASE_RANK = {"sending": 4, "dragging": 3, "waiting": 2, "unconfirmed": 1}


def kind_of(dev):
    if dev.get("type") == "scene":
        return "scene"
    if dev.get("controllable"):
        return "power"
    return "input"


def event_label(ev):
    s = str(ev or "").replace("_", " ")
    return s[:1].upper() + s[1:]


def ago(ms):
    s = max(0, int(ms / 1000 + 0.5))
    if s < 5:
        return "just now"
    if s < 60:
        return "%ds ago" % s
    m = s // 60
    if m < 60:
        return "%d min ago" % m
    h = m // 60
    if h < 24:
        return "%d h ago" % h
    return "%d d ago" % (h // 24)


def dur_label(s):
    if s >= 3600:
        return "%d h" % (s // 3600)
    return "%d min" % (s // 60)


class Intent:
    def __init__(self, slot, kind, name):
        self.slot = slot
        self.kind = kind          # "power" | "bri" | "scene"
        self.name = name
        self.target = None        # bool / None (blind toggle) / int
        self.phase = "sending"
        self.ack_at = 0
        self.in_flight = False
        self.debounce_at = None   # ticks when a debounced value is due
        self.queue = []
        self.expire_at = None     # waiting -> unconfirmed
        self.clear_at = None      # unconfirmed -> dropped

    def live(self):
        return self.phase != "unconfirmed"


def _live(it):
    return it is not None and it.phase != "unconfirmed"


class View:
    """What a tile or the detail sheet shows for one device."""
    __slots__ = ("on", "on_known", "unknown", "bri", "sync", "sub", "kind", "busy", "pw", "br", "cmd", "fault", "recent")


class Model:
    def __init__(self, client, notify):
        self.client = client      # api.Client
        self.notify = notify      # notify(msg, level=None)
        self.devices = {}         # index -> dict, last reported
        self.order = []           # indices in API order
        self.snapshot_at = 0
        self.last_ok_at = 0
        self.last_err = None
        self.structure = None     # changes when the device list changes
        self.version = 0          # bumped on every visible change
        self.intents = {}         # index -> {"power": Intent|None, "bri": Intent|None}
        self.flash = {}           # index -> ticks when "confirmed" flash ends
        self.poll_now = False     # set to request an immediate poll
        self.on_press = lambda dev: None  # a button reported a new press

    # ---------- helpers ----------

    def changed(self):
        self.version += 1

    def get_intent(self, index, slot):
        slots = self.intents.get(index)
        return slots[slot] if slots else None

    def clear_intent(self, index, slot, it=None):
        slots = self.intents.get(index)
        if not slots:
            return
        cur = slots[slot]
        if cur is None or (it is not None and cur is not it):
            return
        slots[slot] = None
        if slots["power"] is None and slots["bri"] is None:
            del self.intents[index]
        self.changed()

    def intent_for(self, dev, slot, kind):
        index = dev["index"]
        slots = self.intents.get(index)
        if slots is None:
            slots = {"power": None, "bri": None}
            self.intents[index] = slots
        it = slots[slot]
        if it is not None and it.kind != kind and not it.in_flight:
            self.clear_intent(index, slot, it)
            slots = self.intents.setdefault(index, {"power": None, "bri": None})
            it = None
        if it is None:
            it = Intent(slot, kind, dev["name"])
            slots[slot] = it
        it.kind = kind
        it.expire_at = None
        it.clear_at = None
        return it

    def has_active_intent(self):
        for slots in self.intents.values():
            for it in slots.values():
                if _live(it):
                    return True
        return False

    def event_age(self, dev):
        return (dev.get("last_event_age_ms") or 0) + ticks_diff(ticks_ms(), self.snapshot_at)

    def sections(self):
        """Non-empty sections as (def, [devices]) in display order."""
        out = []
        known = set()
        for d in SECTIONS:
            known.update(d["types"])
            lst = [self.devices[i] for i in self.order if self.devices[i].get("type") in d["types"]]
            if lst:
                out.append((d, lst))
        other = {}
        for i in self.order:
            t = self.devices[i].get("type")
            if t not in known:
                other.setdefault(t, []).append(self.devices[i])
        for t, lst in other.items():
            out.append(({"key": "other-" + str(t), "title": TYPE_NAME.get(t, str(t)), "types": (t,), "all_off": False}, lst))
        return out

    # ---------- derived view ----------

    def view(self, dev):
        v = View()
        kind = kind_of(dev)
        index = dev["index"]
        pw = self.get_intent(index, "power")
        br = self.get_intent(index, "bri")
        offline = not dev.get("is_healthy", True)
        unknown = (not offline) and bool(dev.get("state_error"))
        on = False if unknown else bool(dev.get("is_on"))
        bri = dev.get("brightness") or 0
        on_known = not unknown

        if not offline:
            if _live(pw):
                if pw.kind == "scene":
                    on = pw.target != 0
                elif pw.target is not None:
                    on = pw.target
                    on_known = True
            if _live(br):
                bri = br.target

        cmd = None
        for it in (pw, br):
            if it is not None and (cmd is None or PHASE_RANK[it.phase] > PHASE_RANK[cmd.phase]):
                cmd = it
        if offline:
            sync = "offline"
        elif cmd is not None:
            sync = cmd.phase
        elif unknown:
            sync = "unknown"
        else:
            sync = "ok"
        busy = sync in ("sending", "waiting", "dragging")

        if kind == "input":
            if offline:
                sub = "Offline"
            elif dev.get("last_event_type"):
                # "Double · 2 min ago": the tile is narrow, the sheet says "press".
                sub = event_label(dev["last_event_type"]).replace(" press", "") + " · " + ago(self.event_age(dev))
            else:
                sub = "No presses yet"
        elif offline:
            sub = "Offline"
        elif busy:
            if sync == "waiting":
                sub = "Confirming…"
            elif cmd.kind == "bri":
                sub = "Dimming…"
            elif cmd.kind == "scene" or cmd.target is None:
                sub = "Switching…"
            else:
                sub = "Turning on…" if on else "Turning off…"
        elif unknown and not on_known:
            sub = "State unknown"
        elif sync == "unconfirmed":
            sub = "Unconfirmed"
        elif kind == "scene":
            sub = dev.get("scene_state") or ("On" if on else "Off")
        else:
            sub = "On" if on else "Off"
            if on and dev.get("has_brightness"):
                sub = "On · %d%%" % bri

        v.on = on
        v.on_known = on_known
        v.unknown = unknown
        v.bri = bri
        v.sync = sync
        v.sub = sub
        v.kind = kind
        v.busy = busy
        v.pw = pw
        v.br = br
        v.cmd = cmd
        v.fault = bool(dev.get("is_faulty")) and not offline
        v.recent = kind == "input" and bool(dev.get("last_event_type")) and self.event_age(dev) < RECENT_PRESS_MS
        return v

    def reported_text(self, dev, it):
        if it.kind == "bri":
            return "it still reports %d%%" % (dev.get("brightness") or 0)
        if it.kind == "scene":
            return "it still reports " + (dev.get("scene_state") or "state %d" % ((dev.get("scene_index") or 0) + 1))
        if dev.get("state_error"):
            return "its state can't be read, so it's not known whether it switched"
        return "it still reports " + ("on" if dev.get("is_on") else "off")

    # ---------- request pump ----------

    def pump(self, index, it):
        if self.get_intent(index, it.slot) is not it or it.in_flight:
            return
        if not it.queue:
            if it.debounce_at is not None:
                it.phase = "dragging"
                self.changed()
                return
            self.acknowledge(index, it)
            return
        job = it.queue.pop(0)
        it.in_flight = True
        it.phase = "sending"
        self.changed()
        asyncio.create_task(self._send(index, it, job))

    async def _send(self, index, it, job):
        try:
            await self.client.post(job["path"], job.get("body"), CMD_TIMEOUT_S)
        except Exception as e:  # noqa: BLE001 - any failure is reported the same way
            it.in_flight = False
            self.fail(index, it, job["verb"], e)
            return
        it.in_flight = False
        if job.get("ack"):
            self.notify(job["ack"])
        self.pump(index, it)

    def acknowledge(self, index, it):
        if self.get_intent(index, it.slot) is not it:
            return
        it.phase = "waiting"
        it.ack_at = ticks_ms()
        it.expire_at = ticks_add(it.ack_at, CONFIRM_MS)
        self.changed()
        self.poll_now = True

    def fail(self, index, it, verb, err):
        self.clear_intent(index, it.slot, it)
        self.notify("Couldn't %s: %s." % (verb, err_text(err)), "error")

    def tick(self):
        """Advances timers; call often from the UI loop."""
        now = ticks_ms()
        for index in list(self.intents):
            slots = self.intents.get(index)
            if not slots:
                continue
            for slot in ("power", "bri"):
                it = slots.get(slot)
                if it is None:
                    continue
                if it.debounce_at is not None and ticks_diff(now, it.debounce_at) >= 0:
                    it.debounce_at = None
                    it.queue = [self._bri_job(index, it)]
                    self.pump(index, it)
                if it.phase == "waiting" and it.expire_at is not None and ticks_diff(now, it.expire_at) >= 0:
                    it.phase = "unconfirmed"
                    it.expire_at = None
                    it.clear_at = ticks_add(now, UNCONFIRMED_HOLD_MS)
                    self.changed()
                    dev = self.devices.get(index)
                    if dev:
                        self.notify("%s didn't confirm the change; %s." % (dev["name"], self.reported_text(dev, it)), "warn")
                elif it.phase == "unconfirmed" and it.clear_at is not None and ticks_diff(now, it.clear_at) >= 0:
                    self.clear_intent(index, slot, it)
        for index in list(self.flash):
            if ticks_diff(now, self.flash[index]) >= 0:
                del self.flash[index]
                self.changed()

    def confirms(self, it, dev, poll_started_at):
        if it.in_flight or it.debounce_at is not None or it.queue:
            return False
        if it.phase in ("sending", "dragging"):
            return False
        if it.phase == "waiting" and ticks_diff(poll_started_at, it.ack_at) < 0:
            return False
        if not dev.get("is_healthy", True):
            return False
        if it.kind == "power":
            if it.target is None:
                return not dev.get("state_error")
            return not dev.get("state_error") and bool(dev.get("is_on")) == it.target
        if it.kind == "bri":
            return abs((dev.get("brightness") or 0) - it.target) <= 1
        if it.kind == "scene":
            return (dev.get("scene_index") or 0) == it.target
        return False

    # ---------- commands ----------

    def set_power(self, dev, target, seconds=None):
        index = dev["index"]
        if not dev.get("is_healthy", True):
            return
        base = "/api/devices/%d/" % index
        name = dev["name"]
        if dev.get("type") == "scene":
            prev = self.get_intent(index, "power")
            frm = prev.target if _live(prev) and prev.kind == "scene" else (dev.get("scene_index") or 0)
            it = self.intent_for(dev, "power", "scene")
            it.target = (frm + 1) % (dev.get("scene_count") or 2)
            it.queue.append({"path": base + "toggle", "verb": "switch " + name})
        else:
            it = self.intent_for(dev, "power", "power")
            it.target = target
            if target is None:
                job = {"path": base + "toggle", "verb": "switch " + name}
            elif seconds:
                job = {
                    "path": base + "set_for",
                    "body": {"value": target, "seconds": seconds},
                    "verb": ("turn on " if target else "turn off ") + name,
                    "ack": "%s %s for %s, then back." % (name, "on" if target else "off", dur_label(seconds)),
                }
            else:
                job = {"path": base + "set", "body": {"value": target}, "verb": ("turn on " if target else "turn off ") + name}
            it.queue = [job]
        it.phase = "sending"
        self.changed()
        self.pump(index, it)

    def tap(self, dev):
        """Primary action on a tile. Returns False if the tap should open the
        detail sheet instead (buttons)."""
        v = self.view(dev)
        if v.kind == "input":
            return False
        if v.sync == "offline":
            self.notify(dev["name"] + " is offline.", "warn")
            return True
        if v.kind == "scene":
            self.set_power(dev, None)
            return True
        if not v.on_known:
            if _live(v.pw) and v.pw.target is None:
                self.notify("Waiting for %s to report its state." % dev["name"], "warn")
                return True
            self.set_power(dev, None)
            return True
        self.set_power(dev, not v.on)
        return True

    def _bri_job(self, index, it):
        return {"path": "/api/devices/%d/set_brightness" % index, "body": {"value": it.target},
                "verb": "set brightness of " + it.name}

    def set_brightness(self, dev, value, immediate):
        index = dev["index"]
        it = self.intent_for(dev, "bri", "bri")
        it.target = max(0, min(100, int(value + 0.5)))
        if immediate:
            it.debounce_at = None
            it.queue = [self._bri_job(index, it)]
            self.pump(index, it)
        else:
            it.debounce_at = ticks_add(ticks_ms(), DEBOUNCE_MS)
            if not it.in_flight:
                it.phase = "dragging"
        self.changed()

    def on_count(self, devs):
        return sum(1 for d in devs if self.view(d).on)

    def all_off(self, devs):
        for d in devs:
            v = self.view(d)
            if v.kind == "power" and v.on and v.sync != "offline":
                self.set_power(d, False)

    # ---------- polling ----------

    def apply_snapshot(self, lst, started_at):
        now = ticks_ms()
        self.snapshot_at = now
        self.last_ok_at = now
        recovered = self.last_err is not None
        self.last_err = None
        nxt = {}
        order = []
        changed = recovered
        presses = []
        for d in lst:
            index = d["index"]
            age = d.get("last_event_age_ms")
            # When the event happened, in local ticks: stable across polls,
            # unlike the age, so it only changes on a new press.
            if age:
                d["_event_at"] = ticks_add(now, -age)
            old = self.devices.get(index)
            if old is None or not _same(old, d):
                changed = True
                if old is not None and age and (not old.get("_event_at") or abs(ticks_diff(d["_event_at"], old["_event_at"])) > 1500):
                    presses.append(d)
            nxt[index] = d
            order.append(index)
        if order != self.order:
            changed = True
        for index in list(self.intents):
            d = nxt.get(index)
            for slot in ("power", "bri"):
                it = self.get_intent(index, slot)
                if it is not None and (d is None or d.get("name") != it.name):
                    self.clear_intent(index, slot, it)
        self.devices = nxt
        self.order = order
        key = "|".join("%s:%s:%s" % (d["index"], d.get("type"), d.get("name")) for d in lst)
        if key != self.structure:
            self.structure = key
        for d in lst:
            for slot in ("power", "bri"):
                it = self.get_intent(d["index"], slot)
                if it is not None and self.confirms(it, d, started_at):
                    was = it.phase == "unconfirmed"
                    self.clear_intent(d["index"], slot, it)
                    self.flash[d["index"]] = ticks_add(now, CONFIRM_FLASH_MS)
                    if was:
                        self.notify(d["name"] + " confirmed the change.")
        if changed:
            self.changed()
        for d in presses:
            self.on_press(d)

    async def poll(self):
        started = ticks_ms()
        try:
            data = await self.client.get("/api/devices", POLL_TIMEOUT_S)
        except Exception as e:  # noqa: BLE001
            first = self.last_err is None
            self.last_err = e
            if first:
                self.changed()
            return False
        if not isinstance(data, list):
            self.last_err = ValueError("unexpected reply")
            self.changed()
            return False
        self.apply_snapshot(data, started)
        return True

    def freshness(self):
        """Returns (level, text): level is connecting | live | aging | stale."""
        if not self.last_ok_at:
            if self.last_err is not None:
                return "stale", "Offline"
            return "connecting", "Connecting…"
        age = ticks_diff(ticks_ms(), self.last_ok_at)
        if age <= STALE_MS:
            if self.last_err is not None:
                return "aging", "Retrying…"
            return "live", "Live"
        return "stale", "Updated " + ago(age)

    def status(self, dev, v):
        """(sync, title, detail) for the detail sheet."""
        level, _ = self.freshness()
        if level == "stale":
            return "stale", "Connection lost", ("Last known state from " + ago(ticks_diff(ticks_ms(), self.last_ok_at))
                                                if self.last_ok_at else "No state received yet.")
        s = v.sync
        if s == "offline":
            return s, "Device offline", "The controller can't reach this device right now."
        if s == "unknown":
            return s, "State unknown", "Couldn't read: " + str(dev.get("state_error") or "")
        if s == "sending":
            return s, "Sending command…", "Waiting for the controller to accept it."
        if s == "dragging":
            return "sending", "Adjusting…", ""
        if s == "waiting":
            return s, "Waiting for the device to confirm", "Accepted " + ago(ticks_diff(ticks_ms(), v.cmd.ack_at)) + "."
        if s == "unconfirmed":
            r = self.reported_text(dev, v.cmd)
            return s, "Not confirmed", r[:1].upper() + r[1:] + ". Try again."
        if v.kind == "input":
            return "ok", "Listening", "Updated " + ago(ticks_diff(ticks_ms(), self.last_ok_at))
        return "ok", "Confirmed by device", "Updated " + ago(ticks_diff(ticks_ms(), self.last_ok_at))


def _same(a, b):
    """Equal for display purposes: the event age ticks on every poll."""
    for k, v in b.items():
        if k in ("last_event_age_ms", "_event_at"):
            continue
        if a.get(k) != v:
            return False
    if len(a) != len(b):
        return False
    ea, eb = a.get("_event_at"), b.get("_event_at")
    if ea is None or eb is None:
        return ea is eb
    return abs(ticks_diff(ea, eb)) <= 1500


def err_text(e):
    name = type(e).__name__
    if name == "TimeoutError" or name == "CancelledError":
        return "no response from the controller"
    if name == "HttpError":
        return str(e)
    if isinstance(e, OSError):
        return "controller unreachable"
    return str(e) or name
