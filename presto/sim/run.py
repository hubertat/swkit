#!/usr/bin/env python3
"""Runs the panel app on a PC against the mock controller and saves
screenshots of what the 480x480 screen would show.

    pip install pillow numpy
    python3 sim/run.py                 # scripted tour -> sim/out/*.png
    python3 sim/run.py --discover      # start from mDNS discovery (faked)

It uses the real src/ modules unchanged; only the hardware modules (presto,
picovector, picographics, network, machine) are replaced by sim/fake/.
"""

import argparse
import asyncio
import os
import shutil
import sys
import tempfile
import threading

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path[:0] = [os.path.join(HERE, "fake"), os.path.join(ROOT, "src"), HERE]

import mock_swkit  # noqa: E402
from http.server import ThreadingHTTPServer  # noqa: E402

PORT = 18080


def start_mock(buttons):
    mock_swkit.init()
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), mock_swkit.Handler)
    mock_swkit.Handler.log_message = lambda *a: None
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


class Driver:
    def __init__(self, app, out):
        self.app = app
        self.out = out
        self.n = 0

    async def wait(self, s):
        await asyncio.sleep(s)

    async def idle(self, s=0.3):
        """Lets the UI catch up (render, polls)."""
        await asyncio.sleep(s)

    async def tap(self, x, y, hold=0.08):
        t = self.app.presto.touch
        t.press(x, y)
        await asyncio.sleep(hold)
        t.release()
        await asyncio.sleep(0.15)

    async def long_press(self, x, y):
        await self.tap(x, y, hold=0.7)

    async def drag(self, x0, y0, x1, y1, ms=300, steps=12):
        t = self.app.presto.touch
        t.press(x0, y0)
        await asyncio.sleep(0.03)
        for i in range(1, steps + 1):
            t.press(x0 + (x1 - x0) * i // steps, y0 + (y1 - y0) * i // steps)
            await asyncio.sleep(ms / 1000 / steps)
        t.release()
        await asyncio.sleep(0.2)

    async def shot(self, name):
        from PIL import Image
        self.n += 1
        await asyncio.sleep(0.15)
        path = os.path.join(self.out, "%02d-%s.png" % (self.n, name))
        Image.fromarray(self.app.presto.front).save(path)
        print("saved", path, "last render %dms" % self.app.ui.render_ms)


async def tour(d):
    await d.wait(3.0)
    await d.shot("lights")
    await d.tap(120, 180)             # Kitchen: off -> on
    await d.wait(0.05)
    await d.shot("lights-pending")
    await d.wait(2.5)
    await d.shot("lights-confirmed")
    await d.drag(240, 360, 240, 140)  # scroll down
    await d.wait(0.6)
    await d.shot("lights-scrolled")
    await d.long_press(120, 240)      # Kids room ceiling (dimmable) detail
    await d.wait(0.4)
    await d.shot("sheet-dimmable")
    await d.drag(240, 380, 240, 160)  # scroll the sheet
    await d.wait(0.5)
    await d.shot("sheet-dimmable-scrolled")
    await d.tap(440, 66)              # close
    await d.wait(0.4)
    await d.drag(400, 250, 100, 250)  # swipe to outlets
    await d.wait(0.6)
    await d.shot("outlets")
    await d.tap(240, 440)             # tab: scenes (2nd of 5 is outlets; layout depends on count)
    await d.wait(0.6)
    await d.shot("tab-tap")
    mock_swkit.DEVICES[14].update(last_event_type="double_press", last_event_time=__import__("time").time())
    mock_swkit.DEVICES[13].update(last_event_type="single_press", last_event_time=__import__("time").time() - 140)
    await d.tap(336, 440)
    await d.wait(2.4)
    await d.shot("buttons")
    await d.tap(120, 180)             # open a button's detail
    await d.wait(0.5)
    await d.shot("sheet-button")
    await d.tap(440, 66)
    await d.wait(0.3)
    await d.tap(432, 440)             # settings
    await d.wait(0.6)
    await d.shot("settings")
    await d.drag(240, 380, 240, 120)
    await d.wait(0.8)
    await d.shot("settings-scrolled")
    # Unreachable controller
    d.mock.shutdown()
    d.mock.server_close()
    await d.tap(48, 440)
    await d.wait(8.5)
    await d.shot("stale")


async def discover(d):
    await d.wait(1.5)
    await d.shot("picker")
    await d.tap(240, 140)
    await d.wait(2.5)
    await d.shot("after-pick")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--discover", action="store_true", help="show the controller picker (fake mDNS results)")
    ap.add_argument("--out", default=os.path.join(HERE, "out"))
    args = ap.parse_args()
    args.out = os.path.abspath(args.out)
    os.makedirs(args.out, exist_ok=True)

    work = tempfile.mkdtemp(prefix="swkit-sim-")
    shutil.copy(os.path.join(ROOT, "assets", "swkit.af"), work)
    with open(os.path.join(work, "secrets.py"), "w") as f:
        f.write('WIFI_SSID = "HomeNet"\nWIFI_PASSWORD = "x"\n')
        if not args.discover:
            f.write('SWKIT_URL = "http://127.0.0.1:%d/control"\n' % PORT)
    os.chdir(work)
    sys.path.insert(0, work)

    mock = start_mock(True)

    if args.discover:
        import mdns

        async def fake_browse(timeout_ms=2500, service=None):
            await asyncio.sleep(0.8)
            return [
                {"name": "Mock home", "host": "vm.local", "ip": "127.0.0.1", "port": PORT, "path": "/control", "api": "1", "ver": ""},
                {"name": "Workshop", "host": "pi-workshop.local", "ip": "192.0.2.55", "port": 8080, "path": "/control", "api": "1", "ver": ""},
            ]
        mdns.browse = fake_browse

    import app as appmod
    app = appmod.App()
    d = Driver(app, args.out)
    d.mock = mock

    async def go():
        asyncio.create_task(app.run())
        await (discover(d) if args.discover else tour(d))

    asyncio.run(go())
    shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
