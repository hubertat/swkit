# Drawing helpers over PicoGraphics + PicoVector.
#
# All text and rounded shapes go through PicoVector (anti-aliased, using the
# custom swkit.af font with Material Symbols icons). Plain rectangles use
# PicoGraphics directly, which is much cheaper. Coordinates passed to the
# PicoVector text calls must be ints (the C binding rejects floats).

from picovector import PicoVector, Polygon, Transform, ANTIALIAS_FAST, HALIGN_LEFT

# Installed next to the modules; the bare name is for the PC simulator.
FONTS = ("/swkit/swkit.af", "swkit.af")

# Material Symbols codepoints packed into swkit.af (see tools/make_font.py).
ICON = {
    "lightbulb": "",
    "outlet": "",
    "scene": "",
    "button": "",
    "settings": "",
    "wifi": "",
    "wifi_off": "",
    "refresh": "",
    "close": "",
    "chevron_left": "",
    "chevron_right": "",
    "timer": "",
    "sync": "",
    "sync_problem": "",
    "cloud_off": "",
    "warning": "",
    "error": "",
    "help": "",
    "check": "",
    "check_circle": "",
    "brightness_low": "",
    "brightness_high": "",
    "remove": "",
    "add": "",
    "dns": "",
    "power": "",
    "search": "",
    "info": "",
    "home": "",
    "light_mode": "",
}

# Cap height of the font as a fraction of its size; used to centre text.
CAP = 0.6


class Gfx:
    def __init__(self, display):
        self.d = display
        self.w, self.h = display.get_bounds()
        self.v = PicoVector(display)
        self.v.set_antialiasing(ANTIALIAS_FAST)
        self.v.set_transform(Transform())
        self.v.set_font_align(HALIGN_LEFT)
        self.font_ok = False
        for path in FONTS:
            try:
                if self.v.set_font(path, 24):
                    self.font_ok = True
                    break
            except OSError:
                pass
        self.v.set_font_word_spacing(100)
        self.size = 24
        self._pens = {}
        self._widths = {}
        self._wraps = {}
        self.clip_rect = (0, 0, self.w, self.h)

    # ---------- pens & clipping ----------

    def pen(self, rgb):
        p = self._pens.get(rgb)
        if p is None:
            p = self.d.create_pen(rgb[0], rgb[1], rgb[2])
            self._pens[rgb] = p
        return p

    def set(self, rgb):
        self.d.set_pen(self.pen(rgb))

    def clip(self, x, y, w, h):
        x, y, w, h = int(x), int(y), int(w), int(h)
        self.clip_rect = (x, y, w, h)
        self.d.set_clip(x, y, w, h)
        self.v.set_clip((x, y, w, h))

    def unclip(self):
        self.clip(0, 0, self.w, self.h)

    def visible(self, x, y, w, h):
        cx, cy, cw, ch = self.clip_rect
        return x < cx + cw and y < cy + ch and x + w > cx and y + h > cy

    # ---------- shapes ----------

    def clear(self, rgb):
        self.set(rgb)
        self.d.clear()

    def rect(self, x, y, w, h, rgb):
        if w <= 0 or h <= 0:
            return
        self.set(rgb)
        self.d.rectangle(int(x), int(y), int(w), int(h))

    def rrect(self, x, y, w, h, r, rgb, stroke=0):
        if w <= 0 or h <= 0 or not self.visible(x, y, w, h):
            return
        self.set(rgb)
        r = min(r, w // 2, h // 2)
        p = Polygon()
        if stroke:
            p.rectangle(int(x), int(y), int(w), int(h), (r, r, r, r), stroke)
        else:
            p.rectangle(int(x), int(y), int(w), int(h), (r, r, r, r))
        self.v.draw(p)

    def circle(self, cx, cy, r, rgb, stroke=0):
        self.set(rgb)
        p = Polygon()
        if stroke:
            p.circle(int(cx), int(cy), int(r), stroke)
        else:
            p.circle(int(cx), int(cy), int(r))
        self.v.draw(p)

    def arc(self, cx, cy, r, a0, a1, rgb, stroke):
        self.set(rgb)
        self.v.draw(Polygon().arc(int(cx), int(cy), int(r), a0, a1, stroke))

    def hatch(self, x, y, w, h, r, rgb, gap=14, thick=5):
        """Diagonal stripes over a rounded box: the "pending" look."""
        ox, oy, ow, oh = self.clip_rect
        p = Polygon()
        k = -h
        while k < w:
            p.path((int(x + k), int(y + h)), (int(x + k + thick), int(y + h)),
                   (int(x + k + thick + h), int(y)), (int(x + k + h), int(y)))
            k += gap
        self.set(rgb)
        # Two passes so stripes stay inside the rounded corners.
        for cx, cy, cw, ch in ((x + r, y, w - 2 * r, h), (x, y + r, w, h - 2 * r)):
            ix, iy = max(cx, ox), max(cy, oy)
            iw, ih = min(cx + cw, ox + ow) - ix, min(cy + ch, oy + oh) - iy
            if iw > 0 and ih > 0:
                self.clip(ix, iy, iw, ih)
                self.v.draw(p)
        self.clip(ox, oy, ow, oh)

    # ---------- text ----------

    def _font(self, size):
        if size != self.size:
            self.v.set_font_size(int(size))
            self.size = size

    def width(self, s, size):
        key = (s, size)
        w = self._widths.get(key)
        if w is None:
            if len(self._widths) > 600:
                self._widths = {}
            self._font(size)
            w = self.v.measure_text(s, 0, 0)[2]
            self._widths[key] = w
        return w

    def fit(self, s, size, max_w):
        """s, shortened with an ellipsis to fit max_w pixels."""
        if max_w <= 0 or self.width(s, size) <= max_w:
            return s
        lo, hi = 0, len(s)
        while lo < hi:
            mid = (lo + hi + 1) // 2
            if self.width(s[:mid].rstrip() + "…", size) <= max_w:
                lo = mid
            else:
                hi = mid - 1
        return s[:lo].rstrip() + "…"

    def wrap(self, s, size, max_w, lines=2):
        """Word-wraps s into at most `lines` lines; the last is ellipsized."""
        key = (s, size, max_w, lines)
        out = self._wraps.get(key)
        if out is not None:
            return out
        if len(self._wraps) > 300:
            self._wraps = {}
        words = s.split(" ")
        out = []
        cur = ""
        i = 0
        while i < len(words):
            cand = (cur + " " + words[i]) if cur else words[i]
            if self.width(cand, size) <= max_w or not cur:
                cur = cand
                i += 1
            else:
                out.append(cur)
                cur = ""
                if len(out) == lines - 1:
                    break
        rest = " ".join([cur] + words[i:]) if cur else " ".join(words[i:])
        if rest:
            out.append(self.fit(rest, size, max_w))
        out = [self.fit(line, size, max_w) for line in out[:lines]]
        self._wraps[key] = out
        return out

    def text(self, s, x, y, size, rgb, align=0, max_w=0):
        """Draws s with its baseline at y. align: 0 left, 1 centre, 2 right
        (relative to x). Ellipsized to max_w when given."""
        if not s:
            return 0
        if max_w:
            s = self.fit(s, size, max_w)
        w = 0
        if align:
            w = self.width(s, size)
            x = x - w // 2 if align == 1 else x - w
        self._font(size)
        self.set(rgb)
        self.v.text(s, int(x), int(y))
        return w

    def text_mid(self, s, x, cy, size, rgb, align=0, max_w=0):
        """Like text(), but vertically centred on cy."""
        return self.text(s, x, cy + int(size * CAP / 2 + 0.5), size, rgb, align, max_w)

    def icon(self, name, cx, cy, box, rgb):
        """Draws an icon centred on (cx, cy); box is the icon's em square."""
        g = ICON.get(name)
        if not g:
            return
        self._font(int(box * 128 / 254 + 0.5))
        self.set(rgb)
        self.v.text(g, int(cx), int(cy))
