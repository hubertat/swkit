# Headless picovector for the MicroPython unix port (sim/mp_check.py).
# Draws nothing; checks argument types the way the C binding does and
# measures text from the real .af font.
import struct

ANTIALIAS_NONE = 0
ANTIALIAS_X4 = ANTIALIAS_FAST = 1
ANTIALIAS_X16 = ANTIALIAS_BEST = 2
HALIGN_LEFT, HALIGN_CENTER, HALIGN_RIGHT = 0, 1, 2

calls = {"text": 0, "draw": 0}


def _int(v):
    if not isinstance(v, int):
        raise TypeError("can't convert %s to int" % type(v).__name__)


def _num(v):
    if not isinstance(v, (int, float)):
        raise TypeError("number required")


class Transform:
    pass


class Polygon:
    def path(self, *pts):
        for p in pts:
            _num(p[0])
            _num(p[1])
        return self

    def rectangle(self, x, y, w, h, corners=None, stroke=None):
        for v in (x, y, w, h):
            _num(v)
        if corners is not None and (type(corners) is not tuple or len(corners) != 4):
            raise ValueError("Corners must have r1, r2, r3, r4")
        return self

    def circle(self, x, y, r, stroke=None):
        for v in (x, y, r):
            _num(v)
        return self

    def arc(self, x, y, r, a0, a1, stroke=None):
        return self


class PicoVector:
    def __init__(self, display):
        self.adv = {}
        self.size = 48

    def set_antialiasing(self, a):
        _int(a)

    def set_transform(self, t):
        pass

    def set_font_align(self, a):
        _int(a)

    def set_font_word_spacing(self, v):
        _int(v)

    def set_font(self, path, size):
        _int(size)
        self.size = size
        try:
            f = open(path, "rb")
        except OSError:
            return False
        d = f.read()
        f.close()
        n = struct.unpack(">H", d[6:8])[0]
        off = 12
        for _ in range(n):
            cp, x, y, w, h, adv, pc = struct.unpack(">HbbBBBB", d[off:off + 8])
            self.adv[cp] = adv
            off += 8
        return True

    def set_font_size(self, size):
        _int(size)
        self.size = size

    def set_clip(self, r):
        if type(r) is not tuple or len(r) != 4:
            raise ValueError("Clip must have x, y, w, h")

    def measure_text(self, text, x=0, y=0, angle=None):
        _int(x)
        _int(y)
        w = 0
        for ch in text:
            a = self.adv.get(ord(ch), 0)
            w += a * (100 if ch == " " else 95) / 100
        return (x, y, int(w * self.size / 128), self.size)

    def text(self, text, x, y, angle=None, max_width=0, max_height=0):
        if not isinstance(text, str):
            raise TypeError("text: string required")
        _int(x)
        _int(y)
        calls["text"] += 1

    def draw(self, poly):
        if not isinstance(poly, Polygon):
            raise TypeError("draw: Polygon required.")
        calls["draw"] += 1
