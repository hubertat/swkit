"""CPython stand-in for Pimoroni's frozen presto.py (simulator only).

The touch controller is driven by the simulator script (sim/run.py) via
`press(x, y)` / `release()`; update() snapshots the framebuffer so the
script can save what the real panel would be showing.
"""

import asyncio
from collections import namedtuple

import numpy as np

from picographics import PicoGraphics

ROTATE_0, ROTATE_180 = 0, 180
touch = namedtuple("touch", ("x", "y", "touched"))

_instance = None


class FT6236:
    def __init__(self, full_res=False, enable_interrupt=False, rotate=0):
        self.x = self.y = 0
        self.x2 = self.y2 = 0
        self.state = False
        self.state2 = False
        self.distance = 0
        self.angle = 0
        self._scale = 1 if full_res else 2
        self._pending = None

    # driven by the simulator
    def press(self, x, y):
        self._pending = (int(x) // self._scale, int(y) // self._scale, True)

    def release(self):
        self._pending = (self.x, self.y, False)

    def poll(self):
        if self._pending:
            self.x, self.y, self.state = self._pending
            self._pending = None


class EzWiFi:
    def __init__(self):
        self._connected = False
        self._handlers = {}

    def on(self, name, handler):
        self._handlers[name] = handler

    async def connect(self, ssid=None, password=None, timeout=60, retries=10):
        await asyncio.sleep(0.3)
        self._connected = True
        h = self._handlers.get("connected")
        if h:
            h(self)
        return True

    def isconnected(self):
        return self._connected

    def ipv4(self):
        return "192.168.1.77"

    def ipv6(self):
        return None

    def error(self):
        return None


class Presto:
    NUM_LEDS = 7

    def __init__(self, full_res=False, palette=False, ambient_light=False, direct_to_fb=False, layers=None, rotate=ROTATE_0):
        global _instance
        _instance = self
        self.wifi = EzWiFi()
        self.width = self.height = 480 if full_res else 240
        self.display = PicoGraphics(self.width, self.height)
        self.touch = FT6236(full_res=full_res, rotate=rotate)
        self.backlight = 1.0
        self.leds = [(0, 0, 0)] * self.NUM_LEDS
        self.auto_ambient = ambient_light
        self.frames = 0
        self.front = np.zeros_like(self.display.fb)
        self.updated_regions = 0

    def set_backlight(self, brightness):
        if not 0.0 <= brightness <= 1.0:
            raise ValueError("brightness out of range. Expected 0.0 to 1.0")
        self.backlight = brightness

    def auto_ambient_leds(self, enable):
        self.auto_ambient = bool(enable)

    def set_led_rgb(self, i, r, g, b):
        self.leds[i] = (int(r), int(g), int(b))

    def set_led_hsv(self, i, h, s=1.0, v=1.0):
        import colorsys
        r, g, b = colorsys.hsv_to_rgb(h, s, v)
        self.leds[i] = (int(r * 255), int(g * 255), int(b * 255))

    def connect(self, ssid=None, password=None):
        self.wifi._connected = True
        return True

    def touch_poll(self):
        self.touch.poll()

    def update(self):
        self.front[:] = self.display.fb
        self.frames += 1
        self.touch.poll()

    def partial_update(self, x, y, w, h):
        self.front[y:y + h, x:x + w] = self.display.fb[y:y + h, x:x + w]
        self.updated_regions += 1
        self.touch.poll()

    def clear(self):
        self.display.clear()
        self.update()


class Buzzer:
    def __init__(self, pin):
        self.freq = 0

    def set_tone(self, freq, duty=0.5):
        self.freq = freq if freq >= 50 else 0
        return freq >= 50
