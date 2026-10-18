# Runs the whole panel app headless on the MicroPython unix port, against
# the mock controller, tapping through every screen. It catches MicroPython
# incompatibilities and API misuse the CPython simulator can't.
#
#   python3 sim/mock_swkit.py --port 18081 &
#   sh sim/mp_check.sh
import asyncio
import sys

PORT = 18081
errors = []


def _excepthook(loop, ctx):
    errors.append(ctx["exception"])
    sys.print_exception(ctx["exception"])


async def tap(t, x, y, hold=0.08):
    t.press(x, y)
    await asyncio.sleep(hold)
    t.release()
    await asyncio.sleep(0.25)


async def drag(t, x0, y0, x1, y1):
    t.press(x0, y0)
    await asyncio.sleep(0.03)
    for i in range(1, 11):
        t.press(x0 + (x1 - x0) * i // 10, y0 + (y1 - y0) * i // 10)
        await asyncio.sleep(0.03)
    t.release()
    await asyncio.sleep(0.4)


SLOT = [(125, 159), (355, 159), (125, 257), (355, 257), (125, 355), (355, 355)]
PAGE_UP, PAGE_DOWN = (436, 170), (436, 344)     # page rail beside the grid
TABS = [(48, 442), (144, 442), (240, 442), (336, 442), (432, 442)]
SHEET_CLOSE = (438, 58)


async def tour(app):
    t = app.presto.touch
    ui = app.ui
    await asyncio.sleep(2.5)
    assert ui.mode == "main", "not connected: %s" % ui.mode
    m = app.model
    print("devices:", len(m.devices))
    # A tap repaints the tile, not the whole screen.
    app.presto.partials = 0
    app.presto.largest = (0, 0)
    await tap(t, *SLOT[0])
    await asyncio.sleep(2.5)
    print("tap + confirm repaints:", app.presto.partials, "largest region:", app.presto.largest)
    assert app.presto.largest[0] * app.presto.largest[1] < 480 * 200, "tap repainted too much: %s" % (app.presto.largest,)
    await drag(t, 240, 330, 240, 150)      # swipe up: next screen
    assert ui.screens.get("lights") == 1, "swipe up did not flip: %s" % ui.screens
    await tap(t, *PAGE_UP)
    assert ui.screens.get("lights") == 0, "up arrow did not flip back"
    await tap(t, *SLOT[1], hold=0.7)       # long press -> sheet
    assert ui.sheet is not None, "sheet did not open"
    await tap(t, 240, 260)
    await drag(t, 240, 200, 240, 420)      # pull down closes the sheet
    assert ui.sheet is None, "pull-down did not close the sheet"
    await drag(t, 400, 250, 100, 250)      # swipe to the next section
    assert ui.section == 1, "swipe left did not change section"
    for x, y in TABS:
        await tap(t, x, y)                 # every tab
        await tap(t, *SLOT[0])
        await tap(t, *SHEET_CLOSE)
    # Settings: every screen, every chip row on the first two.
    await tap(t, *TABS[4])
    for _ in range(4):
        for y in (150, 268, 316, 350):
            await tap(t, 120, y)
        await tap(t, *PAGE_DOWN)
    # Light order: On first puts lit lights first; the order holds while touching.
    office = [d for d in m.devices.values() if d["name"] == "Office desk"][0]
    m.set_power(office, True)                  # the last light in config order
    await asyncio.sleep(2.5)
    app.change_setting("light_sort", "on_first")
    ui.resort()
    lights = [s for s in ui.sections() if s[0] == "lights"][0][4]
    flags = [m.view(d).on for d in lights]
    names = [d["name"] for d in lights]
    print("on-first order:", names)
    assert flags == sorted(flags, reverse=True), "lights not on-first: %s" % flags
    assert names.index("Office desk") < names.index("Bedroom lamp"), "Office desk not moved up"
    # Frozen while the user interacts: turning a light off doesn't reorder
    # until the panel has been left alone for RESORT_IDLE_MS.
    ui.section = 0                             # re-sorting applies to the visible section
    ui.step()
    m.set_power(office, False)
    await asyncio.sleep(2.0)
    ui.last_touch = __import__("time").ticks_ms()
    ui.next_tick = 0
    ui.step()
    still = [d["name"] for d in [s for s in ui.sections() if s[0] == "lights"][0][4]]
    assert still == names, "order changed while in use"
    ui.last_touch -= 6000
    ui.next_tick = 0
    ui.step()
    later = [d["name"] for d in [s for s in ui.sections() if s[0] == "lights"][0][4]]
    assert later[-1] == "Office desk", "idle re-sort didn't happen: %s" % later
    print("idle re-sort ok:", later)
    app.change_setting("light_sort", "name")
    ui.resort()
    names = [d["name"].lower() for d in [s for s in ui.sections() if s[0] == "lights"][0][4]]
    assert names == sorted(names), "A-Z order wrong: %s" % names
    # Settings never touch flash at runtime: they wait in the RAM copy.
    assert app.settings.dirty(), "expected pending settings"
    # A crash inside the UI loop must not freeze the screen any more.
    orig = ui.draw_header
    boom = [0]

    def faulty(*a):
        if not boom[0]:
            boom[0] = 1
            raise ValueError("injected UI fault")
        return orig(*a)
    ui.draw_header = faulty
    ui.invalidate()
    await asyncio.sleep(1.0)
    before = app.presto.partials
    await tap(t, *TABS[0])
    await asyncio.sleep(0.5)
    assert boom[0] and app.presto.partials > before, "UI loop did not recover"
    print("UI loop recovered after an injected fault")
    print("frames", app.presto.frames, "partials", app.presto.partials)
    # What the next boot does before Presto(): commit the RAM copy to flash.
    import settings
    nxt = settings.Settings(ram_dir=settings.RAM_DIR)
    assert nxt["light_sort"] == "name" and nxt.dirty()
    assert nxt.commit() and not nxt.dirty()
    assert settings.Settings()["light_sort"] == "name", "flash copy not updated"
    print("boot commit ok")
    await screen_off(app, t)


async def screen_off(app, t):
    """Screen off: backlight 0, no polls, Wi-Fi power save; a touch only
    wakes it, and the old state reads "Updating…" until a poll answers."""
    import network
    ui, m = app.ui, app.model
    ui.section = 0
    ui.screens = {}
    ui.invalidate()
    await asyncio.sleep(0.3)
    app.change_setting("off_after_s", 300)
    ui.last_touch = __import__("time").ticks_add(ui.last_touch, -301000)
    await asyncio.sleep(1.5)
    assert app.asleep and app.presto.backlight == 0, "screen did not turn off"
    assert network.WLAN.pm == network.WLAN.PM_POWERSAVE, "Wi-Fi not in power save"
    ok_at = m.last_ok_at
    drawn = app.presto.partials
    await asyncio.sleep(2.6)
    assert m.last_ok_at == ok_at, "polled while the screen was off"
    m.changed()
    await asyncio.sleep(0.2)
    assert app.presto.partials == drawn, "drew while the screen was off"
    m.poll_now = False
    first = ui.current()[4][0]
    was_on = m.view(first).on
    t.press(*SLOT[0])                       # lands on a tile: must not switch it
    await asyncio.sleep(0.08)
    assert not app.asleep and app.presto.backlight >= 0.1, "touch did not wake"
    assert app.presto.partials > drawn, "no repaint on wake"
    assert network.WLAN.pm == network.WLAN.PM_PERFORMANCE, "Wi-Fi power save not undone"
    t.release()
    await asyncio.sleep(0.6)
    assert not m.has_active_intent() and m.view(first).on == was_on, "the waking touch switched something"
    assert m.last_ok_at != ok_at and m.freshness()[0] == "live", "no poll after wake: %s" % (m.freshness(),)
    # A settings-commit restart while off comes back off.
    app.sleep()
    app.save_idle_state()
    import app as appmod
    again = appmod.App()
    assert again.asleep and again.presto.backlight == 0, "restart did not keep the screen off"
    app.wake()
    app.change_setting("off_after_s", 0)
    print("screen off ok")


def main():
    import settings
    settings.RAM_DIR = "ramfs"     # a directory standing in for /ramfs
    import app as appmod
    a = appmod.App()
    loop = asyncio.get_event_loop()
    loop.set_exception_handler(_excepthook)

    async def go():
        asyncio.create_task(a.run())
        await tour(a)

    try:
        asyncio.run(go())
    except Exception as e:  # noqa: BLE001
        errors.append(e)
        sys.print_exception(e)
    print("OK" if not errors else "FAILED: %d error(s)" % len(errors))
    sys.exit(1 if errors else 0)


main()
