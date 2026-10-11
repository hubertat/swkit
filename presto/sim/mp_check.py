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


async def tour(app):
    t = app.presto.touch
    await asyncio.sleep(2.5)
    assert app.ui.mode == "main", "not connected: %s" % app.ui.mode
    m = app.model
    print("devices:", len(m.devices))
    await tap(t, 120, 180)                 # toggle
    await asyncio.sleep(2.5)
    await drag(t, 240, 360, 240, 140)      # scroll
    await asyncio.sleep(0.8)
    await tap(t, 120, 240, hold=0.7)       # long press -> sheet
    assert app.ui.sheet is not None, "sheet did not open"
    await tap(t, 300, 200)                 # brightness/whatever is there
    await drag(t, 240, 380, 240, 160)      # scroll sheet
    await tap(t, 440, 66)                  # close
    await drag(t, 400, 250, 100, 250)      # swipe page
    for x in (48, 144, 240, 336, 432):
        await tap(t, x, 440)               # every tab
        await tap(t, 120, 180)
        await tap(t, 440, 66)
    await tap(t, 300, 430 - 76)            # settings controls
    await drag(t, 240, 380, 240, 120)
    for y in (150, 220, 300, 380):
        await tap(t, 120, y)
    await tap(t, 200, 90)                  # Change -> picker
    await asyncio.sleep(3.5)
    print("mode after Change:", app.ui.mode)
    await tap(t, 120, 440)                 # cancel
    await asyncio.sleep(0.5)
    print("frames", app.presto.frames, "partials", app.presto.partials)


def main():
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
