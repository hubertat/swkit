# Touch UI for the swkit panel.
#
# Immediate-mode style: render() walks the whole layout every time it draws,
# registering touch targets ("hot" rects) as it goes, but only spends pixels
# on what intersects the dirty region. A tap on one tile therefore redraws
# one tile, a poll that changed nothing redraws nothing, and the hit targets
# always match what is on screen.
#
# Layout (480x480): header (name + freshness), content (tiles of the current
# page, scrolls vertically, swipe left/right to change page), tab bar. A
# long-press on a tile opens the detail sheet over everything.

from compat import ticks_ms, ticks_diff, ticks_add, gc_collect
from gfx import Gfx
import model as M

W = H = 480
HEADER_H = 60
TAB_H = 76
CONTENT = (0, HEADER_H, W, H - HEADER_H - TAB_H)
MARGIN = 12
GAP = 10
TILE_W = (W - 2 * MARGIN - GAP) // 2
TILE_H = 100
SUMMARY_H = 56
LONG_PRESS_MS = 450
DRAG_PX = 12
SWIPE_PX = 70

# Palette (dark, for a wall panel).
BG = (11, 13, 17)
SURFACE = (29, 33, 41)
SURFACE_2 = (42, 47, 58)
SURFACE_3 = (58, 64, 78)
LINE = (52, 57, 68)
TEXT = (236, 238, 242)
DIM = (152, 158, 170)
FAINT = (98, 104, 116)
INK = (24, 18, 6)            # text on a bright tile
AMBER = (255, 186, 64)       # lights on
TEAL = (70, 204, 160)        # outlets on
VIOLET = (172, 144, 255)     # scene active
BLUE = (96, 156, 255)        # button press, accents
WARN = (255, 168, 38)
ERR = (240, 86, 86)
OK = (74, 200, 120)
MUTED_ON = (120, 112, 92)    # an "on" tile while the page is stale

ON_COLOR = {"light": AMBER, "color_light": AMBER, "dimmable_light": AMBER, "outlet": TEAL, "scene": VIOLET}
TYPE_ICON = {"light": "lightbulb", "color_light": "lightbulb", "dimmable_light": "lightbulb",
             "outlet": "outlet", "scene": "scene", "button": "button"}
SECTION_ICON = {"lights": "lightbulb", "outlets": "outlet", "scenes": "scene", "inputs": "button"}

# Targets that don't light up under the finger.
NO_PRESS = ("backdrop", "sheet_body", "bri_slider", "backlight", "toast")

# Hot-rect flags
F_LONG = 1      # supports long-press
F_SLIDER = 2    # captures drags horizontally

DIM_CHOICES = ((0, "Never"), (30, "30 s"), (120, "2 min"), (600, "10 min"))
AMBIENT_CHOICES = (("off", "Off"), ("status", "Status"), ("glow", "Glow"))
TIMERS = (300, 900, 1800, 3600)
PRESETS = (10, 25, 50, 75, 100)


def _shade(c, k):
    return (min(255, int(c[0] * k)), min(255, int(c[1] * k)), min(255, int(c[2] * k)))


def _union(a, b):
    if a is None:
        return b
    if b is None:
        return a
    x0, y0 = min(a[0], b[0]), min(a[1], b[1])
    x1, y1 = max(a[0] + a[2], b[0] + b[2]), max(a[1] + a[3], b[1] + b[3])
    return (x0, y0, x1 - x0, y1 - y0)


def _inter(a, b):
    x0, y0 = max(a[0], b[0]), max(a[1], b[1])
    x1, y1 = min(a[0] + a[2], b[0] + b[2]), min(a[1] + a[3], b[1] + b[3])
    if x1 <= x0 or y1 <= y0:
        return None
    return (x0, y0, x1 - x0, y1 - y0)


def _inside(r, x, y):
    return r[0] <= x < r[0] + r[2] and r[1] <= y < r[1] + r[3]


class Toast:
    def __init__(self, msg, level, until):
        self.msg = msg
        self.level = level
        self.until = until


class UI:
    def __init__(self, app):
        self.app = app
        self.presto = app.presto
        self.g = Gfx(self.presto.display)
        self.touch = self.presto.touch
        self.mode = "boot"            # boot | picker | main
        self.boot = {"icon": "wifi", "title": "Starting…", "detail": "", "actions": ()}
        self.picker = {"items": [], "scanning": False, "note": ""}
        self.page = 0
        self.scroll = {}              # page key -> px
        self.content_h = {}           # page key -> px
        self.sheet = None             # device index
        self.sheet_scroll = 0
        self.sheet_h = 0
        self.sheet_status = None      # status row rect, refreshed every second
        self.armed = None             # (section key, until) for two-tap All off
        self.toasts = []
        self.hot = []
        self.dirty = (0, 0, W, H)
        self.drawn_version = -1
        self.second_sig = None
        self.pressed = None           # hot entry under the finger
        self.t = None                 # active touch gesture
        self.velocity = 0.0
        self.fling_key = None
        self.dimmed = False
        self.last_touch = ticks_ms()
        self.frame_ms = 0
        self.render_ms = 0
        self.renders = 0

    # ---------- invalidation ----------

    def invalidate(self, rect=None):
        self.dirty = _union(self.dirty, rect or (0, 0, W, H))

    def notify(self, msg, level=None):
        now = ticks_ms()
        self.toasts.append(Toast(msg, level, ticks_add(now, 6000 if level == "error" else 4000)))
        self.toasts = self.toasts[-2:]
        self.invalidate(self.toast_area())

    def toast_area(self):
        return (0, H - TAB_H - 140, W, 140)

    # ---------- pages ----------

    def model(self):
        return self.app.model

    def pages(self):
        """[(key, title, icon, section def or None, devices)]"""
        out = []
        m = self.model()
        if m is not None:
            for d, devs in m.sections():
                out.append((d["key"], d["title"], SECTION_ICON.get(d["key"], "info"), d, devs))
        out.append(("settings", "Settings", "settings", None, []))
        return out

    def go_page(self, i):
        pages = self.pages()
        i = max(0, min(len(pages) - 1, i))
        if i != self.page:
            self.page = i
            self.velocity = 0
            self.app.settings.set("page", i, save=False)
            self.invalidate()

    def show_main(self):
        self.mode = "main"
        n = len(self.pages())
        self.page = min(self.app.settings["page"], n - 1)
        self.invalidate()

    def set_boot(self, icon, title, detail="", actions=()):
        self.mode = "boot"
        self.boot = {"icon": icon, "title": title, "detail": detail, "actions": actions}
        self.invalidate()

    # ---------- main loop ----------

    async def loop(self):
        try:
            import asyncio
        except ImportError:
            import uasyncio as asyncio
        while True:
            t0 = ticks_ms()
            self.step()
            self.frame_ms = ticks_diff(ticks_ms(), t0)
            await asyncio.sleep(0.012 if (self.t or self.velocity) else 0.03)

    def step(self):
        now = ticks_ms()
        m = self.model()
        if m is not None:
            m.tick()
            if m.version != self.drawn_version:
                self.drawn_version = m.version
                self.invalidate()
        self.touch.poll()
        self.handle_touch(now)
        self.animate(now)
        self.per_second(now)
        if self.dirty is not None:
            self.render(self.dirty)
            self.dirty = None
            self.renders += 1
            # A collection on the PSRAM heap takes a few ms; skip it while a
            # finger is moving the content unless garbage piles up.
            if not (self.t or self.velocity) or self.renders % 30 == 0:
                gc_collect()

    def per_second(self, now):
        """Things that age on screen: freshness, "2 min ago", toasts."""
        keep = [t for t in self.toasts if ticks_diff(t.until, now) > 0]
        if len(keep) != len(self.toasts):
            self.toasts = keep
            self.invalidate(self.toast_area())
        if self.armed and ticks_diff(self.armed[1], now) <= 0:
            self.armed = None
            self.invalidate(CONTENT)
        sec = now // 1000
        m = self.model()
        if m is None or self.mode != "main":
            return
        fresh = m.freshness()
        sig = (fresh, sec if self.page_key() == "inputs" or self.sheet is not None or fresh[0] == "stale" else 0)
        if sig != self.second_sig:
            old = self.second_sig
            self.second_sig = sig
            if old is None or old[0][0] != fresh[0]:
                self.invalidate()      # live <-> stale changes the whole look
            else:
                self.invalidate((0, 0, W, HEADER_H))
                if fresh[0] == "stale":
                    self.invalidate((0, HEADER_H, W, 68))   # the banner's "last update"
                if self.page_key() == "inputs":
                    self.invalidate(CONTENT)
                if self.sheet is not None and self.sheet_status:
                    self.invalidate(self.sheet_status)

    def page_key(self):
        pages = self.pages()
        return pages[min(self.page, len(pages) - 1)][0]

    # ---------- touch ----------

    def hit(self, x, y):
        for h in reversed(self.hot):
            if _inside(h[0], x, y):
                return h
        return None

    def handle_touch(self, now):
        tc = self.touch
        down = tc.state
        x, y = tc.x, tc.y
        t = self.t
        if down and t is None:
            self.last_touch = now
            if self.dimmed:
                # The first touch only wakes the screen.
                self.app.wake()
                self.t = {"wake": True}
                return
            self.velocity = 0
            h = self.hit(x, y)
            self.t = {"x0": x, "y0": y, "x": x, "y": y, "t0": now, "h": h, "mode": None,
                      "long": False, "scroll0": self.cur_scroll(), "samples": [(now, y)]}
            if h and h[3] & F_SLIDER:
                self.t["mode"] = "slider"
                self.slider(h, x, False)
            self.set_pressed(h if h and h[1] not in NO_PRESS else None)
            if h and h[1] not in NO_PRESS:
                self.app.click()
            return
        if t is None:
            return
        if t.get("wake"):
            if not down:
                self.t = None
            return
        if down:
            self.last_touch = now
            t["x"], t["y"] = x, y
            t["samples"].append((now, y))
            t["samples"] = t["samples"][-5:]
            dx, dy = x - t["x0"], y - t["y0"]
            if t["mode"] == "slider":
                self.slider(t["h"], x, False)
            elif t["mode"] is None:
                if abs(dx) > DRAG_PX or abs(dy) > DRAG_PX:
                    t["mode"] = "swipe" if abs(dx) > abs(dy) and self.sheet is None and self.mode == "main" else "scroll"
                    self.set_pressed(None)
                elif not t["long"] and t["h"] and t["h"][3] & F_LONG and ticks_diff(now, t["t0"]) >= LONG_PRESS_MS:
                    t["long"] = True
                    self.set_pressed(None)
                    self.long_press(t["h"])
            if t["mode"] == "scroll":
                self.set_scroll(t["scroll0"] - dy)
            return
        # released
        self.t = None
        self.set_pressed(None)
        mode = t["mode"]
        if mode == "slider":
            self.slider(t["h"], t["x"], True)
        elif mode == "scroll":
            s = t["samples"]
            if len(s) >= 2 and ticks_diff(s[-1][0], s[0][0]) > 0:
                v = -(s[-1][1] - s[0][1]) / ticks_diff(s[-1][0], s[0][0]) * 16
                if abs(v) > 2:
                    self.velocity = max(-60, min(60, v))
                    self.fling_key = self.scroll_key()
        elif mode == "swipe":
            dx = t["x"] - t["x0"]
            if dx <= -SWIPE_PX:
                self.go_page(self.page + 1)
            elif dx >= SWIPE_PX:
                self.go_page(self.page - 1)
        elif not t["long"] and t["h"]:
            self.tap(t["h"])

    def set_pressed(self, h):
        if h is self.pressed:
            return
        if self.pressed:
            self.invalidate(self.pressed[0])
        self.pressed = h
        if h:
            self.invalidate(h[0])

    # ---------- scrolling ----------

    def scroll_key(self):
        return "sheet" if self.sheet is not None else ("picker" if self.mode == "picker" else self.page_key())

    def cur_scroll(self):
        k = self.scroll_key()
        return self.sheet_scroll if k == "sheet" else self.scroll.get(k, 0)

    def max_scroll(self, k):
        if k == "sheet":
            return max(0, self.sheet_h - self.sheet_view()[3])
        view = CONTENT[3] if k != "picker" else H - 150
        return max(0, self.content_h.get(k, 0) - view)

    def set_scroll(self, v, k=None):
        k = k or self.scroll_key()
        v = max(0, min(self.max_scroll(k), int(v)))
        if v == (self.sheet_scroll if k == "sheet" else self.scroll.get(k, 0)):
            return False
        if k == "sheet":
            self.sheet_scroll = v
            self.invalidate(self.sheet_view())
        else:
            self.scroll[k] = v
            self.invalidate(CONTENT if self.mode == "main" else (0, 120, W, H - 120))
        return True

    def animate(self, now):
        if self.velocity and self.t is None:
            if self.fling_key != self.scroll_key() or not self.set_scroll(self.cur_scroll() + self.velocity, self.fling_key):
                self.velocity = 0
            else:
                self.velocity *= 0.9
                if abs(self.velocity) < 0.6:
                    self.velocity = 0

    # ---------- actions ----------

    def dev(self, index):
        m = self.model()
        return m.devices.get(index) if m else None

    def tap(self, h):
        action, arg = h[1], h[2]
        m = self.model()
        app = self.app
        if action == "tile":
            d = self.dev(arg)
            if d and not m.tap(d):
                self.open_sheet(arg)
        elif action == "tab":
            self.go_page(arg)
        elif action == "fresh":
            if m:
                m.poll_now = True
        elif action == "alloff":
            key, devs = arg
            if self.armed and self.armed[0] == key:
                self.armed = None
                m.all_off(devs)
            else:
                self.armed = (key, ticks_add(ticks_ms(), 3000))
            self.invalidate(CONTENT)
        elif action == "toast":
            self.toasts = []
            self.invalidate(self.toast_area())
        elif action == "backdrop" or action == "sheet_close":
            self.close_sheet()
        elif action == "power":
            d = self.dev(self.sheet)
            if d:
                m.set_power(d, arg)
        elif action == "timer":
            d = self.dev(self.sheet)
            if d:
                v = m.view(d)
                m.set_power(d, not (v.on_known and v.on), arg)
        elif action == "bri":
            d = self.dev(self.sheet)
            if d:
                m.set_brightness(d, arg, True)
        elif action == "bri_step":
            d = self.dev(self.sheet)
            if d:
                m.set_brightness(d, m.view(d).bri + arg, True)
        elif action == "scene_next":
            d = self.dev(self.sheet)
            if d:
                m.set_power(d, None)
        elif action == "setting":
            key, value = arg
            app.settings.set(key, value)
            app.apply_settings()
            self.invalidate()
        elif action == "find":
            app.start_picker()
        elif action == "pick":
            app.use_server(arg)
        elif action == "rescan":
            app.rescan()
        elif action == "picker_cancel":
            app.cancel_picker()
        elif action == "boot_action":
            arg()

    def long_press(self, h):
        if h[1] == "tile":
            self.open_sheet(h[2])
            self.app.click()

    def slider(self, h, x, final):
        rx, _, rw, _ = h[0]
        pad = 22
        v = (x - rx - pad) / max(1, rw - 2 * pad)
        v = max(0.0, min(1.0, v))
        if h[1] == "bri_slider":
            d = self.dev(self.sheet)
            if d:
                self.model().set_brightness(d, v * 100, final)
        elif h[1] == "backlight":
            level = round(0.1 + v * 0.9, 2)
            self.app.settings.set("backlight", level, save=final)
            self.app.apply_settings()
            self.invalidate(h[0])

    def open_sheet(self, index):
        self.sheet = index
        self.sheet_scroll = 0
        self.invalidate()

    def close_sheet(self):
        self.sheet = None
        self.invalidate()

    def go_home(self):
        """Idle reset: first page, top, no sheet."""
        if self.sheet is not None or self.page or self.scroll.get(self.page_key()):
            self.sheet = None
            self.page = 0
            self.scroll = {}
            self.invalidate()

    # ---------- rendering ----------

    def render(self, region):
        t0 = ticks_ms()
        g = self.g
        self.hot = []
        g.clip(*region)
        g.clear(BG)
        if self.mode == "boot":
            self.draw_boot()
        elif self.mode == "picker":
            self.draw_picker()
        else:
            self.draw_main()
        self.draw_toasts()
        g.unclip()
        x, y, w, h = region
        self.render_ms = ticks_diff(ticks_ms(), t0)
        if w >= W and h >= H:
            self.presto.update()
        else:
            self.presto.partial_update(x, y, w, h)

    def add_hot(self, rect, action, arg=None, flags=0):
        h = (rect, action, arg, flags)
        self.hot.append(h)
        return h

    def is_pressed(self, action, arg):
        p = self.pressed
        return p is not None and p[1] == action and p[2] == arg

    def button(self, x, y, w, h, label, action, arg=None, style="normal", icon=None, size=20):
        g = self.g
        self.add_hot((x, y, w, h), action, arg)
        if not g.visible(x, y, w, h):
            return
        pressed = self.is_pressed(action, arg)
        if style == "primary":
            bg, fg = BLUE, (8, 16, 34)
        elif style == "selected":
            bg, fg = TEXT, BG
        elif style == "danger":
            bg, fg = ERR, (30, 6, 6)
        elif style == "ghost":
            bg, fg = None, TEXT
        else:
            bg, fg = SURFACE_2, TEXT
        if pressed:
            bg = _shade(bg or SURFACE_2, 1.25)
        if bg:
            g.rrect(x, y, w, h, min(16, h // 2), bg)
        if icon:
            iw = int(size * 1.6)
            tw = g.width(g.fit(label, size, w - iw - 24), size) if label else 0
            sx = x + (w - iw - tw - (8 if label else 0)) // 2
            g.icon(icon, sx + iw // 2, y + h // 2, iw + 6, fg)
            if label:
                g.text_mid(label, sx + iw + 8, y + h // 2, size, fg, 0, w - iw - 24)
        else:
            g.text_mid(label, x + w // 2, y + h // 2, size, fg, 1, w - 16)

    def chips(self, x, y, w, h, items, key):
        """Segmented setting: items = ((value, label), ...)."""
        current = self.app.settings[key]
        n = len(items)
        cw = (w - (n - 1) * 8) // n
        for i, (val, label) in enumerate(items):
            self.button(x + i * (cw + 8), y, cw, h, label, "setting", (key, val),
                        "selected" if val == current else "normal", size=18)

    # ----- boot -----

    def draw_boot(self):
        g = self.g
        b = self.boot
        g.circle(W // 2, 150, 54, SURFACE)
        g.icon(b["icon"], W // 2, 150, 76, BLUE if b["icon"] not in ("error", "warning", "wifi_off", "cloud_off") else WARN)
        g.text(b["title"], W // 2, 250, 28, TEXT, 1, W - 40)
        y = 290
        for line in g.wrap(b["detail"], 19, W - 64, 4) if b["detail"] else ():
            g.text(line, W // 2, y, 19, DIM, 1)
            y += 28
        acts = b["actions"]
        if acts:
            bw = (W - 48 - (len(acts) - 1) * 12) // len(acts)
            for i, (label, fn) in enumerate(acts):
                self.button(24 + i * (bw + 12), H - 96, bw, 64, label, "boot_action", fn,
                            "primary" if i == len(acts) - 1 else "normal")

    # ----- picker -----

    def draw_picker(self):
        g = self.g
        p = self.picker
        g.text("Choose a controller", 20, 46, 28, TEXT)
        g.text(p["note"] or ("Searching the network…" if p["scanning"] else "Found on this network:"), 20, 80, 18, DIM, 0, W - 40)
        region = g.clip_rect
        view = (0, 100, W, H - 196)
        items = p["items"]
        y0 = view[1] - self.scroll.get("picker", 0)
        g.clip(*(_inter(view, region) or (0, 0, 0, 0)))
        y = y0
        for i, s in enumerate(items):
            r = (12, y, W - 24, 76)
            self.add_hot(r, "pick", s)
            if g.visible(*r):
                g.rrect(r[0], r[1], r[2], r[3], 16, SURFACE_3 if self.is_pressed("pick", s) else SURFACE)
                g.icon("dns", 50, y + 38, 40, BLUE)
                g.text(s.get("name") or s["host"], 84, y + 34, 22, TEXT, 0, W - 130)
                g.text("%s:%d%s" % (s.get("ip") or s["host"], s["port"], "  ·  current" if s.get("current") else ""), 84, y + 60, 17, DIM, 0, W - 130)
            y += 86
        if not items and not p["scanning"]:
            for line in g.wrap("No swkit controller answered. Set \"Advertise\": true under ControlServer in swkit's config.json, or put SWKIT_URL in secrets.py.", 18, W - 64, 5):
                g.text(line, 32, y + 30, 18, DIM)
                y += 28
        self.content_h["picker"] = y - y0
        g.clip(*region)
        has_cancel = self.app.client is not None
        bw = (W - 36) // 2 if has_cancel else W - 24
        if has_cancel:
            self.button(12, H - 84, bw, 64, "Cancel", "picker_cancel")
        self.button(W - 12 - bw, H - 84, bw, 64, "Searching…" if p["scanning"] else "Scan again", "rescan", None,
                    "normal" if p["scanning"] else "primary", icon="search")

    # ----- main -----

    def draw_main(self):
        g = self.g
        region = g.clip_rect
        m = self.model()
        stale = m.freshness()[0] == "stale"
        pages = self.pages()
        self.page = min(self.page, len(pages) - 1)
        key, title, _, sect, devs = pages[self.page]

        # Content first, clipped to its area, so header and tabs sit on top.
        # An empty clip still registers the content's touch targets.
        g.clip(*(_inter(region, CONTENT) or (0, 0, 0, 0)))
        if key == "settings":
            self.draw_settings()
        else:
            self.draw_tiles(key, sect, devs, stale)
        g.clip(*region)
        self.draw_header(m)
        self.draw_tabs(pages, m)
        if stale:
            self.draw_stale_banner(m)
        if self.sheet is not None:
            self.draw_sheet(m)

    def draw_header(self, m):
        g = self.g
        r = (0, 0, W, HEADER_H)
        if not g.visible(*r):
            self.add_hot((W - 200, 0, 200, HEADER_H), "fresh")
            return
        g.rect(0, 0, W, HEADER_H, BG)
        g.rect(0, HEADER_H - 1, W, 1, LINE)
        level, text = m.freshness()
        dot = {"live": OK, "aging": WARN, "stale": ERR}.get(level, FAINT)
        tw = g.width(text, 17)
        pw = tw + 44
        px = W - 12 - pw
        self.add_hot((px - 10, 0, pw + 22, HEADER_H), "fresh")
        g.rrect(px, 13, pw, 34, 17, SURFACE_3 if self.is_pressed("fresh", None) else SURFACE)
        g.circle(px + 20, 30, 5, dot)
        g.text_mid(text, px + 32, 30, 17, DIM if level == "live" else TEXT)
        g.text_mid(self.app.title(), 18, 30, 26, TEXT, 0, px - 30)

    def draw_tabs(self, pages, m):
        g = self.g
        y = H - TAB_H
        n = len(pages)
        tw = W // n
        for i, (key, title, icon, sect, devs) in enumerate(pages):
            self.add_hot((i * tw, y, tw, TAB_H), "tab", i)
        if not g.visible(0, y, W, TAB_H):
            return
        g.rect(0, y, W, TAB_H, BG)
        g.rect(0, y, W, 1, LINE)
        for i, (key, title, icon, sect, devs) in enumerate(pages):
            cx = i * tw + tw // 2
            active = i == self.page
            col = TEXT if active else FAINT
            if active or self.is_pressed("tab", i):
                g.rrect(cx - 32, y + 9, 64, 36, 18, SURFACE_2 if active else SURFACE)
            g.icon(icon, cx, y + 27, 30, BLUE if active else col)
            g.text(title, cx, y + 66, 15, col, 1, tw - 6)
            if sect and sect["all_off"]:
                on = m.on_count(devs)
                if on:
                    g.circle(cx + 22, y + 12, 10, AMBER if key == "lights" else TEAL)
                    g.text_mid(str(on), cx + 22, y + 12, 13, INK, 1)

    def draw_stale_banner(self, m):
        g = self.g
        r = (8, HEADER_H + 6, W - 16, 56)
        self.add_hot(r, "fresh")
        if not g.visible(0, HEADER_H, W, 68):
            return
        g.rect(0, HEADER_H, W, 68, BG)
        g.rrect(r[0], r[1], r[2], r[3], 14, (70, 30, 30))
        g.icon("cloud_off", 36, r[1] + 28, 30, (255, 170, 170))
        age = M.ago(ticks_diff(ticks_ms(), m.last_ok_at)) if m.last_ok_at else "never"
        g.text("Can't reach the controller", 60, r[1] + 25, 19, TEXT, 0, W - 160)
        g.text("Last update " + age + ". Tap to retry.", 60, r[1] + 46, 15, (230, 190, 190), 0, W - 100)

    # ----- tiles -----

    def draw_tiles(self, key, sect, devs, stale):
        g = self.g
        m = self.model()
        scroll = self.scroll.get(key, 0)
        y0 = CONTENT[1] - scroll + (64 if stale else 0)
        # Summary row
        sy = y0
        if sect and sect["all_off"]:
            on = m.on_count(devs)
            g.text("%d of %d on" % (on, len(devs)) if on else "All off", MARGIN + 4, sy + 36, 20, DIM if on else FAINT)
            if on:
                armed = self.armed and self.armed[0] == key
                label = ("Turn off %d?" % on) if armed else "All off"
                bw = 150 if armed else 112
                self.button(W - MARGIN - bw, sy + 8, bw, 42, label, "alloff", (key, devs), "danger" if armed else "normal",
                            icon=None if armed else "power", size=18)
        elif key == "inputs":
            last = None
            for d in devs:
                if d.get("last_event_type") and (last is None or m.event_age(d) < m.event_age(last)):
                    last = d
            text = "%d buttons" % len(devs)
            if last:
                text += " · last: %s, %s" % (last["name"], M.ago(m.event_age(last)))
            g.text(text, MARGIN + 4, sy + 36, 18, DIM, 0, W - 2 * MARGIN)
        else:
            g.text("%d %s" % (len(devs), sect["title"].lower() if sect else ""), MARGIN + 4, sy + 36, 20, DIM)
        y = y0 + SUMMARY_H
        for i, d in enumerate(devs):
            col = i % 2
            tx = MARGIN + col * (TILE_W + GAP)
            ty = y + (i // 2) * (TILE_H + GAP)
            self.draw_tile(d, tx, ty, stale)
        rows = (len(devs) + 1) // 2
        total = SUMMARY_H + rows * (TILE_H + GAP) + MARGIN + (64 if stale else 0)
        self.content_h[key] = total
        # Scroll indicator
        view = CONTENT[3]
        if total > view and g.visible(W - 6, CONTENT[1], 6, view):
            bar = max(30, view * view // total)
            by = CONTENT[1] + (view - bar) * scroll // max(1, total - view)
            g.rrect(W - 6, by, 4, bar, 2, SURFACE_3)

    def draw_tile(self, d, x, y, stale):
        g = self.g
        m = self.model()
        index = d["index"]
        r = (x, y, TILE_W, TILE_H)
        self.add_hot(r, "tile", index, F_LONG)
        if not g.visible(*r) or not _inter(r, CONTENT):
            return
        v = m.view(d)
        t = d.get("type")
        pressed = self.is_pressed("tile", index)
        oncol = ON_COLOR.get(t, AMBER)
        if stale:
            oncol = MUTED_ON
        offline = v.sync == "offline"
        lit = v.on and v.kind != "input" and not offline
        if v.kind == "input" and v.recent:
            bg, fg, sub_col, icon_col = (32, 58, 104), TEXT, (190, 214, 255), BLUE
        elif lit:
            bg, fg, sub_col, icon_col = oncol, INK, _shade(oncol, 0.42), INK
        elif offline:
            bg, fg, sub_col, icon_col = (22, 25, 31), FAINT, FAINT, FAINT
        else:
            bg, fg, sub_col, icon_col = SURFACE, TEXT, DIM, DIM
        if v.sync == "unconfirmed" or (v.unknown and not v.on_known):
            sub_col = WARN if not lit else (110, 60, 0)
        if pressed:
            bg = _shade(bg, 1.18) if not lit else _shade(bg, 0.86)
        inset = 3 if pressed else 0
        rx, ry, rw, rh = x + inset, y + inset, TILE_W - 2 * inset, TILE_H - 2 * inset
        g.rrect(rx, ry, rw, rh, 18, bg)
        if v.busy:
            g.hatch(rx, ry, rw, rh, 18, _shade(bg, 0.86) if lit else SURFACE_2)
        if v.unknown and not v.on_known and not v.busy:
            g.rrect(rx, ry, rw, rh, 18, FAINT, 2)
        if v.sync == "unconfirmed":
            g.rrect(rx, ry, rw, rh, 18, WARN, 3)
        elif index in m.flash:
            g.rrect(rx, ry, rw, rh, 18, (255, 255, 255) if not lit else INK, 2)

        # Icon in a soft circle
        ix, iy = rx + 38, ry + rh // 2 - (6 if d.get("has_brightness") else 0)
        g.circle(ix, iy, 24, _shade(bg, 0.84) if lit else _shade(bg, 1.35))
        g.icon("help" if (v.unknown and not v.on_known) else TYPE_ICON.get(t, "info"), ix, iy, 34, icon_col)

        tx = rx + 72
        tw = rw - 72 - 12
        right = ""
        if t == "scene" and (d.get("scene_count") or 0) > 2:
            right = "%d/%d" % (((v.pw.target if v.pw and v.pw.kind == "scene" and v.pw.live() else d.get("scene_index")) or 0) + 1,
                               d.get("scene_count"))
        if right:
            g.text(right, rx + rw - 12, ry + 28, 16, sub_col, 2)
        name_w = tw - (g.width(right, 16) + 6 if right else 0)
        lines = g.wrap(d.get("name") or "?", 21, tw, 2)
        if len(lines) == 1:
            lines = [g.fit(lines[0], 21, name_w)]
            g.text(lines[0], tx, ry + rh // 2 - 2, 21, fg)
            g.text(v.sub, tx, ry + rh // 2 + 24, 17, sub_col, 0, tw)
        else:
            lines[0] = g.fit(lines[0], 21, name_w)
            g.text(lines[0], tx, ry + rh // 2 - 14, 21, fg)
            g.text(lines[1], tx, ry + rh // 2 + 10, 21, fg)
            g.text(v.sub, tx, ry + rh // 2 + 34, 16, sub_col, 0, tw)
        if v.fault:
            g.circle(rx + rw - 14, ry + rh - 14, 5, ERR)
        if d.get("has_brightness"):
            bx, bw, by = rx + 16, rw - 32, ry + rh - 12
            g.rrect(bx, by, bw, 5, 2, _shade(bg, 0.8) if lit else SURFACE_2)
            fill = bw * (v.bri if v.on else 0) // 100
            if fill > 4:
                g.rrect(bx, by, fill, 5, 2, INK if lit else DIM)

    # ----- detail sheet -----

    def sheet_view(self):
        return (0, 112, W, H - 112)

    def draw_sheet(self, m):
        g = self.g
        d = self.dev(self.sheet)
        if d is None:
            self.sheet = None
            return
        region = g.clip_rect
        v = m.view(d)
        top = 24
        # Backdrop: everything behind is covered; tap above the card closes.
        self.add_hot((0, 0, W, H), "backdrop")
        g.rect(0, 0, W, top, (5, 6, 8))
        g.rrect(0, top, W, H - top + 30, 26, (24, 27, 34))
        g.rrect(W // 2 - 24, top + 8, 48, 5, 2, SURFACE_3)
        # Card header
        self.add_hot((0, top, W, H - top), "sheet_body")
        self.button(W - 70, top + 18, 56, 56, "", "sheet_close", None, "normal", icon="close", size=22)
        g.text(d.get("name") or "?", 20, top + 50, 28, TEXT, 0, W - 110)
        g.text(M.TYPE_NAME.get(d.get("type"), str(d.get("type"))), 20, top + 78, 17, DIM)

        view = self.sheet_view()
        c = _inter(region, view)
        g.clip(*(c or (0, 0, 0, 0)))
        y0 = view[1] - self.sheet_scroll
        y = y0
        # Status row
        sync, title, detail = m.status(d, v)
        col = {"ok": OK, "sending": BLUE, "waiting": BLUE, "unconfirmed": WARN, "unknown": WARN,
               "offline": ERR, "stale": ERR}.get(sync, FAINT)
        self.sheet_status = _inter((16, y, W - 32, 62), view)
        g.rrect(16, y, W - 32, 62, 14, SURFACE)
        g.circle(38, y + 31, 7, col)
        g.text(title, 56, y + 27, 19, TEXT, 0, W - 90)
        g.text(detail, 56, y + 50, 15, DIM, 0, W - 90)
        y += 78
        if v.kind == "power":
            g.text("Power", 20, y + 16, 16, DIM)
            y += 26
            bw = (W - 32 - 10) // 2
            on_sel = v.on_known and v.on
            off_sel = v.on_known and not v.on
            self.button(16, y, bw, 64, "On", "power", True, "selected" if on_sel else "normal", icon="lightbulb" if d.get("type") != "outlet" else "power", size=21)
            self.button(26 + bw, y, bw, 64, "Off", "power", False, "selected" if off_sel else "normal", icon="power", size=21)
            y += 80
            if d.get("has_brightness"):
                g.text("Brightness", 20, y + 16, 16, DIM)
                g.text("%d%%" % v.bri, W - 20, y + 16, 16, TEXT, 2)
                y += 26
                self.button(16, y, 56, 56, "", "bri_step", -10, icon="remove", size=20)
                self.button(W - 72, y, 56, 56, "", "bri_step", 10, icon="add", size=20)
                sr = (80, y, W - 160, 56)
                self.add_hot(sr, "bri_slider", None, F_SLIDER)
                if g.visible(*sr):
                    pad = 22
                    tx, tw = sr[0] + pad, sr[2] - 2 * pad
                    g.rrect(tx - 4, y + 22, tw + 8, 12, 6, SURFACE_2)
                    k = tx + tw * v.bri // 100
                    g.rrect(tx - 4, y + 22, k - tx + 8, 12, 6, AMBER)
                    g.circle(k, y + 28, 17, TEXT)
                y += 66
                n = len(PRESETS)
                cw = (W - 32 - (n - 1) * 8) // n
                for i, p in enumerate(PRESETS):
                    self.button(16 + i * (cw + 8), y, cw, 46, "%d%%" % p, "bri", p,
                                "selected" if v.bri == p and v.on else "normal", size=17)
                y += 62
            timed_on = not (v.on_known and v.on)
            g.text("Turn %s for, then back" % ("on" if timed_on else "off"), 20, y + 16, 16, DIM)
            y += 26
            n = len(TIMERS)
            cw = (W - 32 - (n - 1) * 8) // n
            for i, s in enumerate(TIMERS):
                self.button(16 + i * (cw + 8), y, cw, 50, M.dur_label(s), "timer", s, icon="timer", size=17)
            y += 66
        elif v.kind == "scene":
            count = d.get("scene_count") or 2
            idx = d.get("scene_index") or 0
            g.text("Active state", 20, y + 16, 16, DIM)
            g.text(d.get("scene_state") or "—", 20, y + 50, 26, TEXT, 0, W - 40)
            y += 66
            dw = min(60, (W - 40 - (count - 1) * 8) // count)
            for i in range(count):
                g.rrect(20 + i * (dw + 8), y, dw, 8, 4, VIOLET if i == idx else SURFACE_2)
            g.text("%d of %d" % (idx + 1, count), W - 20, y + 9, 15, DIM, 2)
            y += 28
            self.button(16, y, W - 32, 68, "Next state", "scene_next", None, "primary", icon="chevron_right", size=22)
            y += 84
        else:
            rows = (("Last press", M.event_label(d["last_event_type"]) if d.get("last_event_type") else "None yet"),
                    ("When", M.ago(m.event_age(d)) if d.get("last_event_type") else "—"),
                    ("Health", "OK" if d.get("is_healthy", True) else "Offline"))
            for label, val in rows:
                g.rrect(16, y, W - 32, 54, 12, SURFACE)
                g.text_mid(label, 32, y + 27, 18, DIM)
                g.text_mid(val, W - 32, y + 27, 20, TEXT, 2, W - 200)
                y += 62
            y += 6
        if d.get("is_faulty"):
            g.text("HomeKit reports a fault on this device.", 20, y + 16, 16, WARN)
            y += 32
        self.sheet_h = y - y0 + 16
        g.clip(*region)

    # ----- settings -----

    def draw_settings(self):
        g = self.g
        app = self.app
        s = app.settings
        y0 = CONTENT[1] - self.scroll.get("settings", 0)
        y = y0 + 12

        def label(text):
            g.text(text.upper(), MARGIN + 6, y + 18, 14, FAINT)

        label("Controller")
        y += 28
        g.rrect(MARGIN, y, W - 2 * MARGIN, 76, 16, SURFACE)
        g.icon("dns", MARGIN + 34, y + 38, 38, BLUE)
        g.text(app.title(), MARGIN + 66, y + 32, 21, TEXT, 0, W - 220)
        g.text(app.client.label() if app.client else "—", MARGIN + 66, y + 58, 16, DIM, 0, W - 220)
        if (app.server or {}).get("fixed"):
            g.text("set in secrets.py", W - MARGIN - 16, y + 44, 15, FAINT, 2)
        else:
            self.button(W - MARGIN - 112, y + 14, 100, 48, "Change", "find", None, size=18)
        y += 92

        label("Screen brightness")
        y += 28
        sr = (MARGIN, y, W - 2 * MARGIN, 56)
        self.add_hot(sr, "backlight", None, F_SLIDER)
        if g.visible(*sr):
            g.rrect(sr[0], sr[1], sr[2], sr[3], 16, SURFACE)
            pad = 22
            tx, tw = sr[0] + pad, sr[2] - 2 * pad
            k = tx + int(tw * (s["backlight"] - 0.1) / 0.9)
            g.rrect(tx - 4, y + 22, tw + 8, 12, 6, SURFACE_2)
            g.rrect(tx - 4, y + 22, k - tx + 8, 12, 6, BLUE)
            g.circle(k, y + 28, 15, TEXT)
        y += 70

        label("Dim when idle")
        y += 28
        self.chips(MARGIN, y, W - 2 * MARGIN, 48, DIM_CHOICES, "dim_after_s")
        y += 62

        label("Back LEDs")
        y += 28
        self.chips(MARGIN, y, W - 2 * MARGIN, 48, AMBIENT_CHOICES, "ambient")
        y += 62

        label("Tap sound")
        y += 28
        self.chips(MARGIN, y, W - 2 * MARGIN, 48, ((False, "Off"), (True, "On")), "click")
        y += 62

        label("Panel")
        y += 28
        info = app.net_info()
        rows = (("Wi-Fi", info.get("ssid") or "—"), ("Signal", info.get("signal") or "—"),
                ("Address", info.get("ip") or "—"), ("Name", info.get("host") or "—"),
                ("Last frame", "%d ms" % self.render_ms), ("App", app.VERSION))
        for k, val in rows:
            g.rect(MARGIN, y, W - 2 * MARGIN, 44, SURFACE)
            g.text_mid(k, MARGIN + 16, y + 22, 17, DIM)
            g.text_mid(val, W - MARGIN - 16, y + 22, 17, TEXT, 2, W - 180)
            y += 45
        y += 16
        self.content_h["settings"] = y - y0

    # ----- toasts -----

    def draw_toasts(self):
        g = self.g
        if not self.toasts:
            return
        y = H - TAB_H - 12
        for t in reversed(self.toasts):
            lines = g.wrap(t.msg, 17, W - 80, 2)
            h = 24 + 22 * len(lines)
            y -= h
            r = (16, y, W - 32, h)
            self.add_hot(r, "toast")
            if g.visible(*r):
                g.rrect(r[0], r[1], r[2], r[3], 14, (46, 50, 60))
                col = {"error": ERR, "warn": WARN}.get(t.level, BLUE)
                g.rrect(r[0] + 8, r[1] + 10, 5, h - 20, 2, col)
                for i, line in enumerate(lines):
                    g.text(line, r[0] + 26, r[1] + 32 + i * 22, 17, TEXT)
            y -= 8
