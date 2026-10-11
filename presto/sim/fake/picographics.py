"""CPython stand-in for a PicoGraphics RGB565 display (simulator only)."""

import numpy as np
from PIL import Image, ImageChops, ImageDraw

SS = 4  # supersampling for anti-aliased fills


class PicoGraphics:
    def __init__(self, width, height):
        self.width, self.height = width, height
        self.fb = np.zeros((height, width, 3), dtype=np.uint8)
        self.pen = (0, 0, 0)
        self.clip = (0, 0, width, height)

    # pens are RGB565 ints, like the real thing
    def create_pen(self, r, g, b):
        for v in (r, g, b):
            if not isinstance(v, int):
                raise TypeError("pen components must be int")
        return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3)

    def set_pen(self, p):
        r = (p >> 8) & 0xF8
        g = (p >> 3) & 0xFC
        b = (p << 3) & 0xF8
        self.pen = (r | r >> 5, g | g >> 6, b | b >> 5)

    def get_bounds(self):
        return (self.width, self.height)

    def set_clip(self, x, y, w, h):
        x0, y0 = max(0, x), max(0, y)
        x1, y1 = min(self.width, x + w), min(self.height, y + h)
        self.clip = (x0, y0, max(0, x1 - x0), max(0, y1 - y0))

    def remove_clip(self):
        self.clip = (0, 0, self.width, self.height)

    def _rect(self, x, y, w, h):
        cx, cy, cw, ch = self.clip
        x0, y0 = max(x, cx), max(y, cy)
        x1, y1 = min(x + w, cx + cw), min(y + h, cy + ch)
        if x1 > x0 and y1 > y0:
            self.fb[y0:y1, x0:x1] = self.pen

    def clear(self):
        self._rect(*self.clip)

    def rectangle(self, x, y, w, h):
        for v in (x, y, w, h):
            if not isinstance(v, int):
                raise TypeError("rectangle: int required")
        self._rect(x, y, w, h)

    def pixel(self, x, y):
        self._rect(x, y, 1, 1)

    def line(self, x1, y1, x2, y2, thickness=1):
        n = max(abs(x2 - x1), abs(y2 - y1), 1)
        for i in range(n + 1):
            x = x1 + (x2 - x1) * i // n
            y = y1 + (y2 - y1) * i // n
            self._rect(x - thickness // 2, y - thickness // 2, thickness, thickness)

    def circle(self, x, y, r):
        yy, xx = np.ogrid[:self.height, :self.width]
        m = (xx - x) ** 2 + (yy - y) ** 2 <= r * r
        cx, cy, cw, ch = self.clip
        c = np.zeros_like(m)
        c[cy:cy + ch, cx:cx + cw] = True
        self.fb[m & c] = self.pen

    def text(self, text, x, y, wordwrap=None, scale=2, angle=0, spacing=1, fixed_width=False):
        im = Image.fromarray(self.fb)
        ImageDraw.Draw(im).text((x, y), str(text), fill=self.pen)
        self.fb[:] = np.asarray(im)

    def measure_text(self, text, scale=2, spacing=1, fixed_width=False):
        return len(str(text)) * 6 * scale

    def set_font(self, name):
        pass

    def _fill(self, paths, vclip, aa):
        """Even-odd polygon fill with coverage-based blending."""
        paths = [p for p in paths if len(p) >= 3]
        if not paths:
            return
        xs = [x for p in paths for x, _ in p]
        ys = [y for p in paths for _, y in p]
        cx, cy, cw, ch = self.clip
        vx, vy, vw, vh = vclip
        x0 = int(max(np.floor(min(xs)), cx, vx, 0))
        y0 = int(max(np.floor(min(ys)), cy, vy, 0))
        x1 = int(min(np.ceil(max(xs)) + 1, cx + cw, vx + vw, self.width))
        y1 = int(min(np.ceil(max(ys)) + 1, cy + ch, vy + vh, self.height))
        if x1 <= x0 or y1 <= y0:
            return
        ss = SS if aa else 1
        size = ((x1 - x0) * ss, (y1 - y0) * ss)
        mask = Image.new("1", size, 0)
        for p in paths:
            tmp = Image.new("1", size, 0)
            ImageDraw.Draw(tmp).polygon([((x - x0) * ss, (y - y0) * ss) for x, y in p], fill=1)
            mask = ImageChops.logical_xor(mask, tmp)
        a = np.asarray(mask.convert("L"), dtype=np.float32) / 255.0
        if ss > 1:
            a = a.reshape(y1 - y0, ss, x1 - x0, ss).mean(axis=(1, 3))
        a = a[..., None]
        region = self.fb[y0:y1, x0:x1].astype(np.float32)
        pen = np.array(self.pen, dtype=np.float32)
        self.fb[y0:y1, x0:x1] = (region * (1 - a) + pen * a + 0.5).astype(np.uint8)
