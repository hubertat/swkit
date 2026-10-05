#!/usr/bin/env python3
"""Builds assets/swkit.af, the PicoVector (Alright Fonts) font the panel uses.

The stock Presto font lacks glyphs the UI needs ("…", "−", a settings gear,
an outlet...), so this packs Roboto Medium (Latin, incl. Polish) together
with the Material Symbols icons listed in ICONS below.

    pip install freetype-py
    python3 tools/make_font.py            # downloads the two source fonts

Both sources are Apache-2.0 licensed (see assets/FONT-LICENSE.md).

Format (as read by pimoroni-pico/libraries/pico_vector/alright-fonts.h):
  "af!?" magic, u16 flags (1 = 16-bit path point counts), u16 glyph count,
  u16 path count, u16 point count; then per glyph u16 codepoint, s8 x, s8 y,
  u8 w, u8 h, u8 advance, u8 path count; then per path its point count; then
  s8 x, s8 y per point. Coordinates are 1/128 of the font size, y down with
  the baseline at 0. Header bounds are y-up (x, yMin, w, h), as Pimoroni's
  fonts store them; they only matter for measure_text.
"""

import argparse
import math
import os
import struct
import sys
import urllib.request

import freetype

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

ROBOTO_URL = "https://raw.githubusercontent.com/googlefonts/roboto-2/main/src/hinted/Roboto-Medium.ttf"
SYMBOLS_URL = "https://raw.githubusercontent.com/pimoroni/presto/main/tools/MaterialSymbolsOutlined-Regular.ttf"

# Roboto units per 128 font units; matches the scale of Pimoroni's Roboto .af.
TEXT_UNITS = 2400

TEXT_RANGES = [(0x20, 0x7E), (0xA0, 0xFF), (0x100, 0x17F)]
TEXT_EXTRA = [0x2013, 0x2014, 0x2022, 0x2026, 0x2190, 0x2192, 0x2212, 0x00B0]

# name -> Material Symbols codepoint. ui.py refers to icons by these names
# (see the ICON table there); keep the two in sync.
ICONS = {
    "lightbulb": 0xE0F0,
    "outlet": 0xF1D4,
    "scene": 0xE65F,          # auto_awesome
    "button": 0xE913,         # touch_app
    "settings": 0xE8B8,
    "wifi": 0xE63E,
    "wifi_off": 0xE648,
    "refresh": 0xE5D5,
    "close": 0xE14C,
    "chevron_left": 0xE408,
    "chevron_right": 0xE409,
    "timer": 0xE425,
    "sync": 0xE627,
    "sync_problem": 0xE629,
    "cloud_off": 0xE2C1,
    "warning": 0xE002,
    "error": 0xE000,
    "help": 0xE887,
    "check": 0xE5CA,
    "check_circle": 0xE86C,
    "brightness_low": 0xE1AD,
    "brightness_high": 0xE1AC,
    "remove": 0xE15B,
    "add": 0xE145,
    "dns": 0xE875,
    "power": 0xE8AC,          # power_settings_new
    "search": 0xE8B6,
    "info": 0xE88E,
    "home": 0xE88A,
    "light_mode": 0xE518,
}


def fetch(url, path):
    if not os.path.exists(path):
        print("downloading", url)
        urllib.request.urlretrieve(url, path)
    return path


class Flattener:
    """Turns a FreeType outline into polylines in output units."""

    def __init__(self, tx):
        self.tx = tx      # font units -> output units
        self.paths = []

    def _pt(self, p):
        return self.tx(p.x, p.y)

    def move_to(self, p, _):
        self.paths.append([self._pt(p)])
        self.last = (p.x, p.y)

    def line_to(self, p, _):
        self.paths[-1].append(self._pt(p))
        self.last = (p.x, p.y)

    def _curve(self, pts):
        a = self.tx(*self.last)
        b = self.tx(*pts[-1])
        ctrl = [self.tx(*q) for q in pts[:-1]]
        length = 0
        prev = a
        for q in ctrl + [b]:
            length += math.hypot(q[0] - prev[0], q[1] - prev[1])
            prev = q
        steps = max(2, min(16, int(math.ceil(length / 5))))
        cps = [self.last] + pts
        for i in range(1, steps + 1):
            t = i / steps
            if len(cps) == 3:
                (x0, y0), (x1, y1), (x2, y2) = cps
                x = (1 - t) ** 2 * x0 + 2 * (1 - t) * t * x1 + t * t * x2
                y = (1 - t) ** 2 * y0 + 2 * (1 - t) * t * y1 + t * t * y2
            else:
                (x0, y0), (x1, y1), (x2, y2), (x3, y3) = cps
                u = 1 - t
                x = u ** 3 * x0 + 3 * u * u * t * x1 + 3 * u * t * t * x2 + t ** 3 * x3
                y = u ** 3 * y0 + 3 * u * u * t * y1 + 3 * u * t * t * y2 + t ** 3 * y3
            self.paths[-1].append(self.tx(x, y))
        self.last = pts[-1]

    def conic_to(self, c, p, _):
        self._curve([(c.x, c.y), (p.x, p.y)])

    def cubic_to(self, c1, c2, p, _):
        self._curve([(c1.x, c1.y), (c2.x, c2.y), (p.x, p.y)])


def clamp8(v):
    return max(-128, min(127, int(round(v))))


def outline(face, cp, tx):
    face.load_char(cp, freetype.FT_LOAD_NO_SCALE | freetype.FT_LOAD_NO_HINTING)
    fl = Flattener(tx)
    face.glyph.outline.decompose(fl, move_to=fl.move_to, line_to=fl.line_to,
                                 conic_to=fl.conic_to, cubic_to=fl.cubic_to)
    paths = []
    for p in fl.paths:
        pts = []
        for x, y in p:
            q = (clamp8(x), clamp8(y))
            if not pts or pts[-1] != q:
                pts.append(q)
        if len(pts) > 1 and pts[0] == pts[-1]:
            pts.pop()
        if len(pts) >= 3:
            paths.append(pts)
    return paths


def glyph_record(cp, paths, advance):
    if paths:
        xs = [x for p in paths for x, _ in p]
        ys = [y for p in paths for _, y in p]
        x0, x1 = min(xs), max(xs)
        y0, y1 = -max(ys), -min(ys)   # y-up bounds, as stored by Pimoroni
    else:
        x0 = x1 = y0 = y1 = 0
    return {
        "cp": cp, "x": clamp8(x0), "y": clamp8(y0),
        "w": min(255, x1 - x0), "h": min(255, y1 - y0),
        "adv": max(0, min(255, int(round(advance)))), "paths": paths,
    }


def build(roboto, symbols):
    glyphs = []

    rf = freetype.Face(roboto)
    s = 128.0 / TEXT_UNITS

    def ttx(x, y):
        return (x * s, -y * s)

    cps = [c for a, b in TEXT_RANGES for c in range(a, b + 1)] + TEXT_EXTRA
    for cp in cps:
        if rf.get_char_index(cp) == 0:
            continue
        paths = outline(rf, cp, ttx)
        glyphs.append(glyph_record(cp, paths, rf.glyph.advance.x * s))

    mf = freetype.Face(symbols)
    em = mf.units_per_EM
    k = 254.0 / em

    def itx(x, y):
        # Centre the em square on the caret, like Pimoroni's icon glyphs.
        return ((x - em / 2) * k, -(y - em / 2) * k)

    for name, cp in ICONS.items():
        if mf.get_char_index(cp) == 0:
            sys.exit("icon %s (U+%04X) missing from %s" % (name, cp, symbols))
        glyphs.append(glyph_record(cp, outline(mf, cp, itx), 255))

    return glyphs


def write(glyphs, path):
    wide = any(len(p) > 255 for g in glyphs for p in g["paths"])
    npaths = sum(len(g["paths"]) for g in glyphs)
    npts = sum(len(p) for g in glyphs for p in g["paths"])
    out = bytearray(b"af!?")
    out += struct.pack(">HHHH", 1 if wide else 0, len(glyphs), npaths, npts)
    for g in glyphs:
        out += struct.pack(">HbbBBBB", g["cp"], g["x"], g["y"], g["w"], g["h"], g["adv"], len(g["paths"]))
    for g in glyphs:
        for p in g["paths"]:
            out += struct.pack(">H" if wide else ">B", len(p))
    for g in glyphs:
        for p in g["paths"]:
            for x, y in p:
                out += struct.pack(">bb", x, y)
    with open(path, "wb") as f:
        f.write(out)
    print("wrote %s: %d glyphs, %d paths, %d points, %d bytes" % (path, len(glyphs), npaths, npts, len(out)))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--roboto", help="Roboto-Medium.ttf (downloaded if omitted)")
    ap.add_argument("--symbols", help="MaterialSymbolsOutlined-Regular.ttf (downloaded if omitted)")
    ap.add_argument("--out", default=os.path.join(ROOT, "assets", "swkit.af"))
    args = ap.parse_args()
    cache = os.path.join(ROOT, "build", "fonts")
    os.makedirs(cache, exist_ok=True)
    roboto = args.roboto or fetch(ROBOTO_URL, os.path.join(cache, "Roboto-Medium.ttf"))
    symbols = args.symbols or fetch(SYMBOLS_URL, os.path.join(cache, "MaterialSymbolsOutlined-Regular.ttf"))
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    write(build(roboto, symbols), args.out)


if __name__ == "__main__":
    main()
