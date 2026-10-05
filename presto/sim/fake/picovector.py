"""CPython stand-in for Pimoroni's picovector module (simulator only).

Mirrors the MicroPython binding's signatures (picovector.cpp in
pimoroni-pico) closely enough to catch API misuse: text()/measure_text()
require int coordinates, Polygon builders chain, fonts are real .af files
rendered with the same layout rules as alright-fonts.h. Filling is even-odd
with 4x4 supersampling, like pretty-poly's X4 antialiasing.
"""

import math
import struct

ANTIALIAS_NONE, ANTIALIAS_X4, ANTIALIAS_X16 = 0, 1, 2
ANTIALIAS_FAST, ANTIALIAS_BEST = 1, 2
HALIGN_LEFT, HALIGN_CENTER, HALIGN_RIGHT = 0, 1, 2
VALIGN_TOP, VALIGN_MIDDLE, VALIGN_BOTTOM = 8, 16, 32


def _need_int(name, v):
    if not isinstance(v, int) or isinstance(v, bool):
        raise TypeError("%s: can't convert %s to int" % (name, type(v).__name__))


class Transform:
    def __init__(self):
        self.m = [1, 0, 0, 0, 1, 0]

    def reset(self):
        self.m = [1, 0, 0, 0, 1, 0]


class Polygon:
    def __init__(self):
        self.paths = []

    def path(self, *pts):
        self.paths.append([(float(x), float(y)) for x, y in pts])
        return self

    def rectangle(self, x, y, w, h, corners=None, stroke=None):
        r = tuple(corners) if corners is not None else (0, 0, 0, 0)
        if len(r) != 4:
            raise ValueError("Corners must have r1, r2, r3, r4")
        self.paths.append(_rrect(x, y, w, h, r))
        if stroke:
            s = stroke
            self.paths.append(_rrect(x + s, y + s, w - 2 * s, h - 2 * s, tuple(max(0, c - s) for c in r)))
        return self

    def circle(self, x, y, r, stroke=None):
        n = max(8, int(r * 1.5))
        self.paths.append([(x + math.cos(i / n * 2 * math.pi) * r, y + math.sin(i / n * 2 * math.pi) * r) for i in range(n)])
        if stroke:
            ri = r - stroke
            self.paths.append([(x + math.cos(i / n * 2 * math.pi) * ri, y + math.sin(i / n * 2 * math.pi) * ri) for i in range(n)])
        return self

    def arc(self, x, y, r, a0, a1, stroke=None):
        n = max(6, int(abs(a1 - a0) / 6))
        outer = [(x + math.sin(math.radians(a0 + (a1 - a0) * i / n)) * r,
                  y - math.cos(math.radians(a0 + (a1 - a0) * i / n)) * r) for i in range(n + 1)]
        ri = r - (stroke or r)
        inner = [(x + math.sin(math.radians(a0 + (a1 - a0) * i / n)) * ri,
                  y - math.cos(math.radians(a0 + (a1 - a0) * i / n)) * ri) for i in range(n, -1, -1)]
        self.paths.append(outer + inner)
        return self

    def line(self, x, y, x2, y2, thickness=None):
        t = (thickness or 1) / 2
        dx, dy = x2 - x, y2 - y
        ln = math.hypot(dx, dy) or 1
        nx, ny = -dy / ln * t, dx / ln * t
        self.paths.append([(x + nx, y + ny), (x2 + nx, y2 + ny), (x2 - nx, y2 - ny), (x - nx, y - ny)])
        return self


def _rrect(x, y, w, h, r):
    r1, r2, r3, r4 = r
    pts = []

    def corner(cx, cy, rad, q):
        steps = math.ceil(rad / 5) + 2
        th = math.pi / 2 * q
        for i in range(steps + 1):
            pts.append((cx + math.sin(th) * rad, cy + math.cos(th) * rad))
            th -= (math.pi / 2) / steps

    corner(x + r1, y + r1, r1, 3) if r1 else pts.append((x, y))
    corner(x + w - r2, y + r2, r2, 2) if r2 else pts.append((x + w, y))
    corner(x + w - r3, y + h - r3, r3, 1) if r3 else pts.append((x + w, y + h))
    corner(x + r4, y + h - r4, r4, 0) if r4 else pts.append((x, y + h))
    return pts


class _Font:
    def __init__(self, path):
        d = open(path, "rb").read()
        if d[:4] != b"af!?":
            raise ValueError("not an af font")
        flags, n, _, npts = struct.unpack(">HHHH", d[4:12])
        off = 12
        gl = []
        for _ in range(n):
            cp, x, y, w, h, adv, pc = struct.unpack(">HbbBBBB", d[off:off + 8])
            off += 8
            gl.append([cp, x, y, w, h, adv, pc, []])
        for g in gl:
            for _ in range(g[6]):
                if flags & 1:
                    c = struct.unpack(">H", d[off:off + 2])[0]
                    off += 2
                else:
                    c = d[off]
                    off += 1
                g[7].append(c)
        pts = struct.unpack(">%db" % (npts * 2), d[off:off + npts * 2])
        pi = 0
        self.glyphs = {}
        for g in gl:
            paths = []
            for c in g[7]:
                paths.append([(pts[pi + 2 * k], pts[pi + 2 * k + 1]) for k in range(c)])
                pi += 2 * c
            self.glyphs[g[0]] = {"x": g[1], "y": g[2], "w": g[3], "h": g[4], "adv": g[5], "paths": paths}


class PicoVector:
    def __init__(self, graphics):
        self.g = graphics
        self.font = None
        self.size = 48
        self.letter = 95
        self.word = 200
        self.line_height = 110
        self.align = HALIGN_LEFT
        self.aa = ANTIALIAS_NONE
        self.clip = (0, 0, graphics.width, graphics.height)

    def set_antialiasing(self, aa):
        self.aa = aa

    def set_transform(self, t):
        pass

    def set_font(self, path, size):
        _need_int("size", size)
        try:
            self.font = _Font(path)
        except OSError:
            self.font = None
            return False
        self.size = size
        return True

    def set_font_size(self, size):
        _need_int("size", size)
        self.size = size

    def set_font_letter_spacing(self, v):
        self.letter = v

    def set_font_word_spacing(self, v):
        self.word = v

    def set_font_line_height(self, v):
        self.line_height = v

    def set_font_align(self, a):
        self.align = a

    def set_clip(self, r):
        if not isinstance(r, tuple) or len(r) != 4:
            raise ValueError("Clip must have x, y, w, h")
        self.clip = r

    def _layout(self, text):
        if self.font is None:
            return []
        s = self.size / 128
        out = []
        cx = 0
        for ch in text:
            cp = ord(ch)
            if cp > 0xFFFF:
                continue
            g = self.font.glyphs.get(cp)
            if g is None:
                continue
            out.append((cx, g))
            cx += g["adv"] * (self.word if ch == " " else self.letter) / 100
        return [(x * s, g) for x, g in out]

    def measure_text(self, text, x=0, y=0, angle=None):
        _need_int("x", x)
        _need_int("y", y)
        s = self.size / 128
        lay = self._layout(text)
        if not lay:
            return (0, 0, 0, 0)
        x0 = min(cx + g["x"] * s for cx, g in lay)
        x1 = max(cx + (g["x"] + g["w"]) * s for cx, g in lay)
        y0 = min(g["y"] * s for _, g in lay)
        y1 = max((g["y"] + g["h"]) * s for _, g in lay)
        return (int(x0 + x), int(y0 + y), int(x1 - x0), int(y1 - y0))

    def text(self, text, x, y, angle=None, max_width=0, max_height=0):
        _need_int("x", x)
        _need_int("y", y)
        s = self.size / 128
        paths = []
        for cx, g in self._layout(text):
            for p in g["paths"]:
                paths.append([(x + cx + px * s, y + py * s) for px, py in p])
        self.g._fill(paths, self.clip, self.aa)

    def draw(self, poly):
        if not isinstance(poly, Polygon):
            raise TypeError("draw: Polygon required.")
        self.g._fill(poly.paths, self.clip, self.aa)
