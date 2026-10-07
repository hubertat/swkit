# Tests for src/model.py: the command / confirmation state machine.
# Runs on CPython and on the MicroPython unix port:
#   python3 sim/test_model.py
#   MICROPYPATH=.frozen:src micropython sim/test_model.py
import sys

sys.path.insert(0, "src")

try:
    import asyncio
except ImportError:
    import uasyncio as asyncio

import model as M

clock = [100000]
M.ticks_ms = lambda: clock[0]
M.ticks_diff = lambda a, b: a - b
M.ticks_add = lambda a, b: a + b


def advance(ms):
    clock[0] += ms


class FakeClient:
    def __init__(self):
        self.sent = []          # (path, body)
        self.gate = None        # Event: when set, posts are held until it fires
        self.fail = None
        self.in_flight = 0
        self.max_in_flight = 0

    async def post(self, path, body=None, timeout=8.0):
        self.in_flight += 1
        self.max_in_flight = max(self.max_in_flight, self.in_flight)
        try:
            if self.gate is not None:
                await self.gate.wait()
            await asyncio.sleep(0)
            if self.fail:
                raise self.fail
            self.sent.append((path, body))
            return {}
        finally:
            self.in_flight -= 1

    async def get(self, path, timeout=5.0):
        return []


def dev(index=0, **kw):
    d = {"index": index, "name": "Lamp %d" % index, "type": "light", "is_on": False, "is_healthy": True,
         "controllable": True, "has_brightness": False, "brightness": 0}
    d.update(kw)
    return d


def setup(*devs):
    toasts = []
    c = FakeClient()
    m = M.Model(c, lambda msg, level=None: toasts.append((level, msg)))
    m.apply_snapshot([dict(d) for d in devs], clock[0])
    return m, c, toasts


async def settle():
    for _ in range(10):
        await asyncio.sleep(0)


async def test_set_waits_for_confirmation():
    m, c, _ = setup(dev())
    m.tap(m.devices[0])
    v = m.view(m.devices[0])
    assert v.on and v.sync == "sending", (v.on, v.sync)
    await settle()
    assert c.sent == [("/api/devices/0/set", {"value": True})], c.sent
    assert m.view(m.devices[0]).sync == "waiting"
    # A poll that started before the acknowledgement may carry the old value:
    # it must neither confirm nor flip the tile back.
    m.apply_snapshot([dev()], clock[0] - 10)
    v = m.view(m.devices[0])
    assert v.on and v.sync == "waiting", (v.on, v.sync)
    # A later poll reporting the new state confirms it.
    advance(500)
    m.apply_snapshot([dev(is_on=True)], clock[0])
    v = m.view(m.devices[0])
    assert v.on and v.sync == "ok", (v.on, v.sync)
    assert 0 in m.flash


async def test_rapid_taps_are_serialized_latest_wins():
    m, c, _ = setup(dev())
    c.gate = asyncio.Event()
    m.set_power(m.devices[0], True)
    await settle()
    m.set_power(m.devices[0], False)
    m.set_power(m.devices[0], True)
    m.set_power(m.devices[0], False)
    await settle()
    assert c.in_flight == 1 and c.sent == []
    c.gate.set()
    await settle()
    await settle()
    assert c.max_in_flight == 1, c.max_in_flight
    assert c.sent == [("/api/devices/0/set", {"value": True}), ("/api/devices/0/set", {"value": False})], c.sent
    assert not m.view(m.devices[0]).on


async def test_unconfirmed_then_cleared():
    m, c, toasts = setup(dev())
    m.tap(m.devices[0])
    await settle()
    advance(M.CONFIRM_MS + 1)
    m.tick()
    v = m.view(m.devices[0])
    assert v.sync == "unconfirmed" and not v.on, (v.sync, v.on)
    assert toasts and toasts[-1][0] == "warn" and "still reports off" in toasts[-1][1], toasts
    assert not m.has_active_intent()
    advance(M.UNCONFIRMED_HOLD_MS + 1)
    m.tick()
    assert m.view(m.devices[0]).sync == "ok"


async def test_unknown_state_toggles_once():
    m, c, toasts = setup(dev(state_error="stale"))
    v = m.view(m.devices[0])
    assert v.unknown and not v.on_known and v.sub == "State unknown", v.sub
    m.tap(m.devices[0])
    m.tap(m.devices[0])
    await settle()
    assert c.sent == [("/api/devices/0/toggle", {})] or c.sent == [("/api/devices/0/toggle", None)], c.sent
    assert toasts and toasts[-1][0] == "warn", toasts


async def test_power_tap_keeps_queued_brightness():
    # Brightness 30% in flight, 80% queued, then On: 80% must still be sent
    # and the brightness slot must not drop the pending power state.
    m, c, _ = setup(dev(type="dimmable_light", has_brightness=True, brightness=10))
    c.gate = asyncio.Event()
    m.set_brightness(m.devices[0], 30, True)
    await settle()
    m.set_brightness(m.devices[0], 80, True)
    m.set_power(m.devices[0], True)
    await settle()
    assert m.view(m.devices[0]).on, "pending power state lost"
    c.gate.set()
    for _ in range(4):
        await settle()
    bri = [b["value"] for p, b in c.sent if p.endswith("set_brightness")]
    pw = [b["value"] for p, b in c.sent if p.endswith("/set")]
    assert bri == [30, 80], c.sent
    assert pw == [True], c.sent


async def test_unconfirmed_on_unreadable_device_stays_unknown():
    # After expiry an unreadable device must not be presented as "off".
    m, c, toasts = setup(dev(state_error="stale"))
    m.tap(m.devices[0])
    await settle()
    advance(M.CONFIRM_MS + 1)
    m.tick()
    m.apply_snapshot([dev(state_error="stale")], clock[0])   # polls keep failing to read it
    d = m.devices[0]
    v = m.view(d)
    assert v.sync == "unconfirmed" and v.unknown and not v.on_known, (v.sync, v.unknown, v.on_known)
    assert v.sub == "State unknown", v.sub
    _, title, detail = m.status(d, v)
    assert "reports off" not in detail and "can't be read" in detail, detail
    assert "reports off" not in toasts[-1][1], toasts


async def test_brightness_debounced():
    m, c, _ = setup(dev(type="dimmable_light", has_brightness=True, is_on=True, brightness=40))
    for v in (45, 50, 62, 70):
        m.set_brightness(m.devices[0], v, False)
        advance(20)
        m.tick()
    assert m.view(m.devices[0]).bri == 70
    assert c.sent == []
    advance(M.DEBOUNCE_MS)
    m.tick()
    await settle()
    assert c.sent == [("/api/devices/0/set_brightness", {"value": 70})], c.sent
    # Power and brightness are independent slots.
    m.set_power(m.devices[0], False)
    await settle()
    assert m.get_intent(0, "bri") is not None
    assert c.sent[-1] == ("/api/devices/0/set", {"value": False})


async def test_scene_steps_queue():
    m, c, _ = setup(dev(type="scene", scene_index=0, scene_count=3, scene_state="Off"))
    c.gate = asyncio.Event()
    m.tap(m.devices[0])
    m.tap(m.devices[0])
    await settle()
    assert m.get_intent(0, "power").target == 2
    c.gate.set()
    await settle()
    await settle()
    assert len(c.sent) == 2 and all(p.endswith("/toggle") for p, _ in c.sent), c.sent


async def test_failure_reverts_and_reports():
    m, c, toasts = setup(dev())
    c.fail = OSError("boom")
    m.tap(m.devices[0])
    await settle()
    v = m.view(m.devices[0])
    assert not v.on and v.sync == "ok", (v.on, v.sync)
    assert toasts[-1][0] == "error" and "unreachable" in toasts[-1][1], toasts


async def test_offline_is_not_tappable():
    m, c, toasts = setup(dev(is_healthy=False))
    m.tap(m.devices[0])
    await settle()
    assert c.sent == [] and m.view(m.devices[0]).sub == "Offline"


async def test_button_press_detected_once():
    b = dev(type="button", controllable=False)
    m, c, _ = setup(b)
    presses = []
    m.on_press = presses.append
    v0 = m.version
    advance(2000)
    m.apply_snapshot([dict(b)], clock[0])
    assert m.version == v0, "unchanged snapshot must not redraw"
    m.apply_snapshot([dict(b, last_event_type="single_press", last_event_age_ms=100)], clock[0])
    assert len(presses) == 1
    advance(2000)
    m.apply_snapshot([dict(b, last_event_type="single_press", last_event_age_ms=2100)], clock[0])
    assert len(presses) == 1, "same press seen again"
    advance(2000)
    m.apply_snapshot([dict(b, last_event_type="single_press", last_event_age_ms=50)], clock[0])
    assert len(presses) == 2


async def test_reload_drops_mismatched_intents():
    m, c, _ = setup(dev())
    c.gate = asyncio.Event()
    m.tap(m.devices[0])
    m.apply_snapshot([dev(name="Other")], clock[0])
    assert m.get_intent(0, "power") is None
    c.gate.set()
    await settle()


def run():
    tests = [(k, v) for k, v in sorted(globals().items()) if k.startswith("test_")]
    failed = 0
    for name, fn in tests:
        try:
            asyncio.run(fn())
            print("ok  ", name)
        except Exception as e:  # noqa: BLE001
            failed += 1
            print("FAIL", name, repr(e))
    print("%d passed, %d failed" % (len(tests) - failed, failed))
    sys.exit(1 if failed else 0)


run()
