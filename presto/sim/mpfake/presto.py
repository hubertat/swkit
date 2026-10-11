# Headless presto module for the MicroPython unix port (sim/mp_check.py).
import asyncio


class _Display:
    def __init__(self, w, h):
        self.w, self.h = w, h

    def get_bounds(self):
        return (self.w, self.h)

    def create_pen(self, r, g, b):
        for v in (r, g, b):
            if not isinstance(v, int):
                raise TypeError("pen components must be int")
        return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3)

    def set_pen(self, p):
        if not isinstance(p, int):
            raise TypeError("pen must be int")

    def set_clip(self, x, y, w, h):
        for v in (x, y, w, h):
            if not isinstance(v, int):
                raise TypeError("set_clip: int required")

    def clear(self):
        pass

    def rectangle(self, x, y, w, h):
        for v in (x, y, w, h):
            if not isinstance(v, int):
                raise TypeError("rectangle: int required")


class FT6236:
    def __init__(self):
        self.x = self.y = 0
        self.state = False
        self._pending = None

    def press(self, x, y):
        self._pending = (x, y, True)

    def release(self):
        self._pending = (self.x, self.y, False)

    def poll(self):
        if self._pending:
            self.x, self.y, self.state = self._pending
            self._pending = None


class EzWiFi:
    async def connect(self, ssid=None, password=None, timeout=60, retries=10):
        await asyncio.sleep(0.1)
        return True

    def isconnected(self):
        return True

    def ipv4(self):
        return "192.168.1.77"

    def error(self):
        return None, None


class Presto:
    def __init__(self, full_res=False, palette=False, ambient_light=False, direct_to_fb=False, layers=None, rotate=0):
        self.width = self.height = 480 if full_res else 240
        self.display = _Display(self.width, self.height)
        self.touch = FT6236()
        self.wifi = EzWiFi()
        self.frames = 0
        self.partials = 0

    def set_backlight(self, b):
        if not 0.0 <= b <= 1.0:
            raise ValueError("brightness out of range")

    def auto_ambient_leds(self, e):
        pass

    def set_led_rgb(self, i, r, g, b):
        for v in (i, r, g, b):
            if not isinstance(v, int):
                raise TypeError("set_led_rgb: int required")

    def update(self):
        self.frames += 1
        self.touch.poll()

    def partial_update(self, x, y, w, h):
        for v in (x, y, w, h):
            if not isinstance(v, int):
                raise TypeError("partial_update: int required")
        self.partials += 1
        self.touch.poll()


class Buzzer:
    def __init__(self, pin):
        pass

    def set_tone(self, f, duty=0.5):
        return f >= 50
