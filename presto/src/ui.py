# Touch UI for the swkit panel.
#
# Immediate-mode style: render() walks the layout of what is on screen,
# registering touch targets ("hot" rects) as it goes, but only spends pixels
# on what intersects the dirty region.
#
# Drawing on the Presto is the expensive part (anti-aliased shapes and text
# rendered into a PSRAM framebuffer), so the UI is built to draw as little
# as possible without lowering quality:
# - no free scrolling: a section shows up to six tiles per screen and longer
#   sections are paged (swipe up/down or the arrows), one redraw per flip;
# - the model reports which devices changed, so a tap or a poll repaints
#   only those tiles, plus the summary bar and tab badges if their counts
#   changed;
# - while a finger is down, background repaints (polls, timers) wait, so the
#   pressed tile responds first;
# - per-second refreshes ("2 min ago") repaint only text that changed.
#
# Layout (480x480): header (name, freshness; red when the controller is
# unreachable), a bar with the section summary, All off and the screen pager,
# a 2x3 tile grid, and the tab bar. A long-press opens the detail sheet.

from compat import ticks_ms, ticks_diff, ticks_add
from gfx import Gfx
import model as M

W = H = 480
HEADER_H = 60
TAB_H = 76
CONTENT = (0, HEADER_H, W, H - HEADER_H - TAB_H)      # bar + grid
BAR = (0, HEADER_H, W, 52)
GRID = (0, HEADER_H + 52, W, CONTENT[3] - 52)
TABS = (0, H - TAB_H, W, TAB_H)
HEADER = (0, 0, W, HEADER_H)
MARGIN = 12
GAP = 8
COLS, ROWS = 2, 3
PER_SCREEN = COLS * ROWS
TILE_W = (W - 2 * MARGIN - GAP) // COLS
TILE_H = 90
LONG_PRESS_MS = 450
DRAG_PX = 14
SWIPE_PX = 60

SHEET_TOP = 16
SHEET_BODY = (0, SHEET_TOP + 84, W, H - SHEET_TOP - 84)
PICKER_LIST = (0, 96, W, H - 96 - 96)
PICKER_ROW = 84

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
MUTED_ON = (120, 112, 92)    # an "on" tile while the controller is unreachable
STALE_BG = (74, 28, 28)

ON_COLOR = {"light": AMBER, "color_light": AMBER, "dimmable_light": AMBER, "outlet": TEAL, "scene": VIOLET}
TYPE_ICON = {"light": "lightbulb", "color_light": "lightbulb", "dimmable_light": "lightbulb",
             "outlet": "outlet", "scene": "scene", "button": "button"}
SECTION_ICON = {"lights": "lightbulb", "outlets": "outlet", "scenes": "scene", "inputs": "button"}

# Targets that don't light up under the finger.
NO_PRESS = ("backdrop", "sheet_body", "bri_slider", "backlight", "toast")

# Hot-rect flags
F_LONG = 1      # supports long-press
F_SLIDER = 2    # captures horizontal drags

DIM_CHOICES = ((0, "Never"), (30, "30 s"), (120, "2 min"), (600, "10 min"))
AMBIENT_CHOICES = (("off", "Off"), ("status", "Status"), ("glow", "Glow"))
SORT_CHOICES = (("config", "Config"), ("on_first", "On first"), ("name", "A–Z"))
RESORT_IDLE_MS = 5000   # "On first" re-sorts only after this long without touches
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


def _add_rect(rects, r):
    """Adds r to a list of dirty rects, merging it with any it overlaps.
    Separate rects stay separate, so a tile plus the tab bar repaints two
    small areas instead of their bounding box. Many rects collapse to one."""
    if rects is None:
        rects = []
    i = 0
    while i < len(rects):
        if _inter(rects[i], r):
            r = _union(rects.pop(i), r)
            i = 0
            continue
        i += 1
    rects.append(r)
    if len(rects) > 6:
        u = None
        for q in rects:
            u = _union(u, q)
        rects = [u]
    return rects


def _inside(r, x, y):
    return r[0] <= x < r[0] + r[2] and r[1] <= y < r[1] + r[3]


def tile_rect(slot):
    """Screen rect of tile slot 0..5 in the grid."""
    col, row = slot % COLS, slot // COLS
    return (MARGIN + col * (TILE_W + GAP), GRID[1] + 2 + row * (TILE_H + GAP), TILE_W, TILE_H)


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
        self.boot = {"icon": "wifi", "title": "Starting...", "detail": "", "actions": ()}
        self.picker = {"items": [], "scanning": False, "note": ""}
        self.section = 0              # index into sections()
        self.screens = {}             # section key -> screen index
        self.orders = {}              # section key -> device indices in display order
        self.sheet = None             # device index
        self.armed = None             # (section key, until) for two-tap All off
        self.toasts = []
        self.hot = []
        self.dirty = [(0, 0, W, H)]   # rects to repaint
        self.deferred = None          # background repaints held while a finger is down
        self.drawn_version = -1
        self.drawn = {}               # things drawn that age: key -> text
        self.tile_rects = {}          # device index -> rect, for the screen on display
        self.badges = None
        self.summary = None
        self.next_tick = 0
        self.pressed = None           # hot entry under the finger
        self.t = None                 # active touch gesture
        self.dimmed = False
        self.last_touch = ticks_ms()
        self.render_ms = 0

    # ---------- invalidation ----------

    def invalidate(self, rect=None, background=False):
        """Marks a region for repaint. Background repaints (polls, timers)
        wait while a finger is down so touch feedback comes first."""
        rect = rect or (0, 0, W, H)
        t = self.t
        if background and t is not None and not t.get("wake") and t.get("mode") != "slider":
            self.deferred = _add_rect(self.deferred, rect)
        else:
            self.dirty = _add_rect(self.dirty, rect)

    def notify(self, msg, level=None):
        now = ticks_ms()
        self.toasts.append(Toast(msg, level, ticks_add(now, 6000 if level == "error" else 4000)))
        self.toasts = self.toasts[-2:]
        self.invalidate(self.toast_area())

    def toast_area(self):
        return (0, H - TAB_H - 140, W, 140)

    # ---------- sections and screens ----------

    def model(self):
        return self.app.model

    def sections(self):
        """[(key, title, icon, section def or None, devices)]"""
        out = []
        m = self.model()
        if m is not None:
            for d, devs in m.sections():
                out.append((d["key"], d["title"], SECTION_ICON.get(d["key"], "info"), d, self.ordered(d["key"], devs)))
        out.append(("settings", "Settings", "settings", None, []))
        return out

    def sort_key(self, mode):
        m = self.model()
        if mode == "name":
            return lambda d: ((d.get("name") or "").lower(), d["index"])
        # On first; ties keep swkit's config order.
        return lambda d: (0 if m.view(d).on else 1, m.order.index(d["index"]) if d["index"] in m.order else 0)

    def ordered(self, key, devs):
        """Devices of a section in display order. Lights follow the
        light_sort setting; "On first" is frozen between re-sorts so a tile
        never moves under the finger (see per_tick)."""
        mode = self.app.settings["light_sort"] if key == "lights" else "config"
        if mode == "config":
            return devs
        by_index = {}
        for d in devs:
            by_index[d["index"]] = d
        cached = self.orders.get(key)
        if cached is None or len(cached) != len(devs) or any(i not in by_index for i in cached):
            cached = [d["index"] for d in sorted(devs, key=self.sort_key(mode))]
            self.orders[key] = cached
        return [by_index[i] for i in cached]

    def resort(self):
        """Forget frozen orders; the next draw sorts afresh."""
        self.orders = {}

    def current(self):
        secs = self.sections()
        self.section = min(self.section, len(secs) - 1)
        return secs[self.section]

    def section_key(self):
        return self.current()[0]

    def screen_count(self, key, devs):
        if key == "settings":
            return len(self.settings_screens())
        return max(1, (len(devs) + PER_SCREEN - 1) // PER_SCREEN)

    def screen_of(self, key, count):
        s = min(self.screens.get(key, 0), count - 1)
        self.screens[key] = s
        return s

    def go_section(self, i):
        secs = self.sections()
        i = max(0, min(len(secs) - 1, i))
        if i != self.section:
            self.section = i
            self.resort()
            self.invalidate()
        elif self.screens.get(secs[i][0]):
            self.screens[secs[i][0]] = 0        # tapping the active tab: back to the top
            self.invalidate()

    def flip(self, delta):
        """Next/previous screen of the current section (or of the picker)."""
        if self.mode == "picker":
            n = self.picker_screens()
            s = max(0, min(n - 1, self.screens.get("picker", 0) + delta))
            if s != self.screens.get("picker", 0):
                self.screens["picker"] = s
                self.invalidate()
            return
        key, _, _, _, devs = self.current()
        n = self.screen_count(key, devs)
        s = max(0, min(n - 1, self.screens.get(key, 0) + delta))
        if s != self.screens.get(key, 0):
            self.screens[key] = s
            self.resort()
            self.invalidate((0, HEADER_H, W, H - HEADER_H - TAB_H))

    def show_main(self):
        self.mode = "main"
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
            self.step()
            await asyncio.sleep(0.01 if self.t else 0.02)

    def step(self):
        now = ticks_ms()
        # Touch first: the finger's feedback is drawn before anything else.
        self.touch.poll()
        self.handle_touch(now)
        m = self.model()
        if m is not None:
            m.tick()
            if m.version != self.drawn_version:
                self.drawn_version = m.version
                self.model_changed(m.take_dirty())
        self.per_tick(now)
        if self.dirty:
            regions = self.dirty
            self.dirty = None
            for region in regions:
                self.render(region)

    def model_changed(self, indices):
        if self.mode != "main":
            return
        if indices is None:
            self.invalidate(background=True)
            return
        for i in indices:
            r = self.tile_rects.get(i)
            if r:
                self.invalidate(r, background=True)
            if self.sheet == i:
                self.invalidate(SHEET_BODY, background=self.t is None or self.t.get("mode") != "slider")
        badges = self.badge_counts()
        if badges != self.badges:
            self.badges = badges
            self.invalidate(TABS, background=True)
        summary = self.summary_text()
        if summary != self.summary:
            self.summary = summary
            self.invalidate(BAR, background=True)

    def badge_counts(self):
        m = self.model()
        return tuple(m.on_count(devs) if sect and sect["all_off"] else 0 for _, _, _, sect, devs in self.sections())

    def summary_text(self):
        key, _, _, sect, devs = self.current()
        m = self.model()
        if sect and sect["all_off"]:
            on = m.on_count(devs)
            return ("%d of %d on" % (on, len(devs))) if on else "All off"
        if key == "inputs":
            last = None
            for d in devs:
                if d.get("last_event_type") and (last is None or m.event_age(d) < m.event_age(last)):
                    last = d
            if last:
                return "Last: %s, %s" % (last["name"], M.ago(m.event_age(last)))
            return "%d buttons" % len(devs)
        if key == "settings":
            return "Settings"
        return "%d %s" % (len(devs), sect["title"].lower() if sect else "")

    def per_tick(self, now):
        """Things that age on screen ("2 min ago", freshness, toasts) are
        checked twice a second and repainted only if their text changed."""
        keep = [t for t in self.toasts if ticks_diff(t.until, now) > 0]
        if len(keep) != len(self.toasts):
            self.toasts = keep
            self.invalidate(self.toast_area())
        if self.armed and ticks_diff(self.armed[1], now) <= 0:
            self.armed = None
            self.invalidate(BAR)
        if ticks_diff(now, self.next_tick) < 0:
            return
        self.next_tick = ticks_add(now, 500)
        m = self.model()
        if m is None or self.mode != "main":
            return
        fresh = m.freshness()
        old = self.drawn.get("fresh")
        if old != fresh:
            if old is None or (old[0] == "stale") != (fresh[0] == "stale"):
                self.invalidate(background=True)    # the stale look covers everything
            else:
                self.invalidate(HEADER, background=True)
        if self.sheet is not None:
            d = self.dev(self.sheet)
            if d is not None and m.status(d, m.view(d)) != self.drawn.get("status"):
                self.invalidate(SHEET_BODY, background=True)
            return
        key = self.section_key()
        if (key == "lights" and self.app.settings["light_sort"] == "on_first" and self.t is None
                and ticks_diff(now, self.last_touch) >= RESORT_IDLE_MS and "lights" in self.orders):
            m_devs = self.current()[4]
            fresh_order = [d["index"] for d in sorted(m_devs, key=self.sort_key("on_first"))]
            if fresh_order != self.orders["lights"]:
                self.orders["lights"] = fresh_order
                self.invalidate(CONTENT, background=True)
        if key == "inputs":
            summary = self.summary_text()
            if summary != self.summary:
                self.summary = summary
                self.invalidate(BAR, background=True)
            for index, r in self.tile_rects.items():
                d = self.dev(index)
                if d is not None:
                    v = m.view(d)
                    if (v.sub, v.recent) != self.drawn.get(("tile", index)):
                        self.invalidate(r, background=True)
        elif key == "settings" and self.screens.get("settings", 0) == len(self.settings_screens()) - 1:
            # The last settings screen shows signal and frame time.
            if self.drawn.get("panel") != self.panel_rows():
                self.invalidate(GRID, background=True)

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
            h = self.hit(x, y)
            self.t = {"x0": x, "y0": y, "x": x, "y": y, "t0": now, "h": h, "mode": None, "long": False}
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
            dx, dy = x - t["x0"], y - t["y0"]
            if t["mode"] == "slider":
                self.slider(t["h"], x, False)
            elif t["mode"] is None:
                if abs(dx) > DRAG_PX or abs(dy) > DRAG_PX:
                    t["mode"] = "swipe"
                    self.set_pressed(None)
                elif not t["long"] and t["h"] and t["h"][3] & F_LONG and ticks_diff(now, t["t0"]) >= LONG_PRESS_MS:
                    t["long"] = True
                    self.set_pressed(None)
                    self.long_press(t["h"])
            return
        # released
        self.t = None
        self.set_pressed(None)
        if self.deferred:
            for r in self.deferred:
                self.dirty = _add_rect(self.dirty, r)
            self.deferred = None
        mode = t["mode"]
        if mode == "slider":
            self.slider(t["h"], t["x"], True)
        elif mode == "swipe":
            self.swipe(t["x"] - t["x0"], t["y"] - t["y0"])
        elif not t["long"] and t["h"]:
            self.tap(t["h"])

    def swipe(self, dx, dy):
        if abs(dx) >= abs(dy):
            if abs(dx) < SWIPE_PX or self.mode != "main" or self.sheet is not None:
                return
            self.go_section(self.section + (1 if dx < 0 else -1))
        else:
            if abs(dy) < SWIPE_PX:
                return
            if self.sheet is not None:
                if dy > 0:
                    self.close_sheet()       # pull the sheet down to close it
                return
            if self.mode in ("main", "picker"):
                self.flip(1 if dy < 0 else -1)

    def set_pressed(self, h):
        if h is self.pressed:
            return
        if self.pressed:
            self.invalidate(self.pressed[0])
        self.pressed = h
        if h:
            self.invalidate(h[0])

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
            self.go_section(arg)
        elif action == "flip":
            self.flip(arg)
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
            self.invalidate(BAR)
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
            app.change_setting(key, value)
            if key == "light_sort":
                self.resort()
                self.invalidate(TABS)
            self.invalidate(CONTENT)
        elif action == "restart":
            app.restart("Saving settings")
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
            self.app.change_setting("backlight", level, final)
            self.invalidate(h[0])

    def open_sheet(self, index):
        self.sheet = index
        self.invalidate()

    def close_sheet(self):
        self.sheet = None
        self.invalidate()

    def go_home(self):
        """Idle reset: first section, first screen, no sheet."""
        if self.sheet is not None or self.section or any(self.screens.values()):
            self.sheet = None
            self.section = 0
            self.screens = {}
            self.resort()
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
        # A plain copy for full frames too: update() waits for the scan-out
        # to pass, which costs up to a frame for no visible gain here.
        self.presto.partial_update(x, y, w, h)
        self.render_ms = ticks_diff(ticks_ms(), t0)

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
        elif style == "disabled":
            bg, fg = SURFACE, FAINT
        else:
            bg, fg = SURFACE_2, TEXT
        if pressed and style != "disabled":
            bg = _shade(bg, 1.25)
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

    def pager(self, right, cy, screen, count):
        """Up / "2/3" / down, right-aligned at x=right. Returns its left edge."""
        if count <= 1:
            return right
        g = self.g
        bw, bh = 52, 44
        x = right - bw
        self.button(x, cy - bh // 2, bw, bh, "", "flip", 1, "disabled" if screen >= count - 1 else "normal",
                    icon="expand_more", size=20)
        x -= 44
        g.text_mid("%d/%d" % (screen + 1, count), x + 22, cy, 17, DIM, 1)
        x -= bw
        self.button(x, cy - bh // 2, bw, bh, "", "flip", -1, "disabled" if screen <= 0 else "normal",
                    icon="expand_less", size=20)
        return x

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

    def picker_screens(self):
        per = PICKER_LIST[3] // PICKER_ROW
        return max(1, (len(self.picker["items"]) + per - 1) // per)

    def draw_picker(self):
        g = self.g
        p = self.picker
        count = self.picker_screens()
        screen = self.screen_of("picker", count)
        left = self.pager(W - 12, 40, screen, count)
        g.text("Choose a controller", 20, 46, 28, TEXT, 0, left - 30)
        g.text(p["note"] or ("Searching the network..." if p["scanning"] else "Found on this network:"), 20, 82, 18, DIM, 0, W - 40)
        per = PICKER_LIST[3] // PICKER_ROW
        items = p["items"][screen * per:(screen + 1) * per]
        y = PICKER_LIST[1]
        for s in items:
            r = (12, y, W - 24, 76)
            self.add_hot(r, "pick", s)
            if g.visible(*r):
                g.rrect(r[0], r[1], r[2], r[3], 16, SURFACE_3 if self.is_pressed("pick", s) else SURFACE)
                g.icon("dns", 50, y + 38, 40, BLUE)
                g.text(s.get("name") or s["host"], 84, y + 34, 22, TEXT, 0, W - 130)
                g.text("%s:%d%s" % (s.get("ip") or s["host"], s["port"], "  ·  current" if s.get("current") else ""), 84, y + 60, 17, DIM, 0, W - 130)
            y += PICKER_ROW
        if not p["items"] and not p["scanning"]:
            for line in g.wrap("No swkit controller answered. Set \"Advertise\": true under ControlServer in swkit's config.json, or put SWKIT_URL in secrets.py.", 18, W - 64, 5):
                g.text(line, 32, y + 30, 18, DIM)
                y += 28
        has_cancel = self.app.client is not None
        bw = (W - 36) // 2 if has_cancel else W - 24
        if has_cancel:
            self.button(12, H - 84, bw, 64, "Cancel", "picker_cancel")
        self.button(W - 12 - bw, H - 84, bw, 64, "Searching..." if p["scanning"] else "Scan again", "rescan", None,
                    "normal" if p["scanning"] else "primary", icon="search")

    # ----- main -----

    def draw_main(self):
        g = self.g
        region = g.clip_rect
        m = self.model()
        stale = m.freshness()[0] == "stale"
        secs = self.sections()
        key, title, _, sect, devs = self.current()
        self.tile_rects = {}
        g.clip(*(_inter(region, CONTENT) or (0, 0, 0, 0)))
        if key == "settings":
            self.draw_settings()
        else:
            self.draw_tiles(key, sect, devs, stale)
        g.clip(*region)
        self.draw_header(m, stale)
        self.draw_tabs(secs, m)
        if self.sheet is not None:
            self.draw_sheet(m)

    def draw_header(self, m, stale):
        g = self.g
        fresh = m.freshness()
        self.drawn["fresh"] = fresh
        self.add_hot((W - 200, 0, 200, HEADER_H) if not stale else HEADER, "fresh")
        if not g.visible(*HEADER):
            return
        if stale:
            g.rect(0, 0, W, HEADER_H, STALE_BG)
            g.icon("cloud_off", 32, 30, 34, (255, 170, 170))
            g.text("Can't reach " + self.app.title(), 60, 27, 20, TEXT, 0, W - 160)
            age = M.ago(ticks_diff(ticks_ms(), m.last_ok_at)) if m.last_ok_at else "never"
            g.text("Last update %s · tap to retry" % age, 60, 49, 15, (235, 195, 195), 0, W - 160)
            self.button(W - 96, 10, 84, 40, "Retry", "fresh", None, "normal", size=17)
            return
        g.rect(0, HEADER_H - 1, W, 1, LINE)
        level, text = fresh
        dot = {"live": OK, "aging": WARN}.get(level, FAINT)
        tw = g.width(text, 17)
        pw = tw + 44
        px = W - 12 - pw
        g.rrect(px, 13, pw, 34, 17, SURFACE_3 if self.is_pressed("fresh", None) else SURFACE)
        g.circle(px + 20, 30, 5, dot)
        g.text_mid(text, px + 32, 30, 17, DIM if level == "live" else TEXT)
        g.text_mid(self.app.title(), 18, 30, 26, TEXT, 0, px - 30)

    def draw_tabs(self, secs, m):
        g = self.g
        y = TABS[1]
        n = len(secs)
        tw = W // n
        for i in range(n):
            self.add_hot((i * tw, y, tw, TAB_H), "tab", i)
        if not g.visible(*TABS):
            return
        g.rect(0, y, W, 1, LINE)
        counts = self.badge_counts()
        self.badges = counts
        for i, (key, title, icon, sect, devs) in enumerate(secs):
            cx = i * tw + tw // 2
            active = i == self.section
            col = TEXT if active else FAINT
            if active or self.is_pressed("tab", i):
                g.rrect(cx - 32, y + 9, 64, 36, 18, SURFACE_2 if active else SURFACE)
            g.icon(icon, cx, y + 27, 30, BLUE if active else col)
            g.text(title, cx, y + 66, 15, col, 1, tw - 6)
            if counts[i]:
                g.circle(cx + 22, y + 12, 10, AMBER if key == "lights" else TEAL)
                g.text_mid(str(counts[i]), cx + 22, y + 12, 13, INK, 1)

    # ----- tiles -----

    def draw_tiles(self, key, sect, devs, stale):
        g = self.g
        m = self.model()
        count = self.screen_count(key, devs)
        screen = self.screen_of(key, count)
        cy = BAR[1] + BAR[3] // 2
        left = self.pager(W - MARGIN, cy, screen, count)
        if sect and sect["all_off"] and m.on_count(devs):
            armed = self.armed and self.armed[0] == key
            bw = 150 if armed else 108
            left -= bw + 8
            self.button(left, cy - 22, bw, 44, ("Turn off %d?" % m.on_count(devs)) if armed else "All off",
                        "alloff", (key, devs), "danger" if armed else "normal",
                        icon=None if armed else "power", size=17)
        self.summary = self.summary_text()
        g.text_mid(self.summary, MARGIN + 4, cy, 19, DIM, 0, left - MARGIN - 16)
        for slot, d in enumerate(devs[screen * PER_SCREEN:(screen + 1) * PER_SCREEN]):
            self.draw_tile(d, tile_rect(slot), stale)

    def draw_tile(self, d, r, stale):
        g = self.g
        m = self.model()
        index = d["index"]
        self.add_hot(r, "tile", index, F_LONG)
        self.tile_rects[index] = r
        v = m.view(d)
        self.drawn[("tile", index)] = (v.sub, v.recent)
        if not g.visible(*r):
            return
        x, y = r[0], r[1]
        t = d.get("type")
        pressed = self.is_pressed("tile", index)
        oncol = MUTED_ON if stale else ON_COLOR.get(t, AMBER)
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

        bri_bar = d.get("has_brightness")
        mid = ry + rh // 2 - (4 if bri_bar else 0)
        g.circle(rx + 36, mid, 23, _shade(bg, 0.84) if lit else _shade(bg, 1.35))
        g.icon("help" if (v.unknown and not v.on_known) else TYPE_ICON.get(t, "info"), rx + 36, mid, 32, icon_col)

        tx = rx + 68
        tw = rw - 68 - 10
        right = ""
        if t == "scene" and (d.get("scene_count") or 0) > 2:
            right = "%d/%d" % (((v.pw.target if v.pw and v.pw.kind == "scene" and v.pw.live() else d.get("scene_index")) or 0) + 1,
                               d.get("scene_count"))
            g.text(right, rx + rw - 12, ry + 24, 15, sub_col, 2)
        name_w = tw - (g.width(right, 15) + 6 if right else 0)
        lines = g.wrap(d.get("name") or "?", 20, tw, 2)
        if len(lines) == 1:
            g.text(g.fit(lines[0], 20, name_w), tx, mid - 1, 20, fg)
            g.text(v.sub, tx, mid + 22, 16, sub_col, 0, tw)
        else:
            g.text(g.fit(lines[0], 20, name_w), tx, mid - 12, 20, fg)
            g.text(lines[1], tx, mid + 9, 20, fg)
            g.text(v.sub, tx, mid + 30, 15, sub_col, 0, tw)
        if v.fault:
            g.circle(rx + rw - 14, ry + 14, 5, ERR)
        if bri_bar:
            bx, bw, by = rx + 16, rw - 32, ry + rh - 10
            g.rrect(bx, by, bw, 4, 2, _shade(bg, 0.8) if lit else SURFACE_2)
            fill = bw * (v.bri if v.on else 0) // 100
            if fill > 4:
                g.rrect(bx, by, fill, 4, 2, INK if lit else DIM)

    # ----- detail sheet -----

    def draw_sheet(self, m):
        """Fits one screen: no scrolling. Pull down or tap above to close."""
        g = self.g
        d = self.dev(self.sheet)
        if d is None:
            self.sheet = None
            return
        v = m.view(d)
        top = SHEET_TOP
        self.add_hot((0, 0, W, H), "backdrop")
        self.add_hot((0, top, W, H - top), "sheet_body")
        if g.visible(0, 0, W, top + 84):
            g.rect(0, 0, W, top, (5, 6, 8))
        g.rrect(0, top, W, H - top + 30, 26, (24, 27, 34))
        if g.visible(0, top, W, 84):
            g.rrect(W // 2 - 24, top + 8, 48, 5, 2, SURFACE_3)
            g.text(d.get("name") or "?", 20, top + 48, 26, TEXT, 0, W - 110)
            g.text(M.TYPE_NAME.get(d.get("type"), str(d.get("type"))), 20, top + 72, 16, DIM)
        self.button(W - 70, top + 14, 56, 56, "", "sheet_close", None, "normal", icon="close", size=22)

        y = SHEET_BODY[1]
        status = m.status(d, v)
        self.drawn["status"] = status
        sync, title, detail = status
        col = {"ok": OK, "sending": BLUE, "waiting": BLUE, "unconfirmed": WARN, "unknown": WARN,
               "offline": ERR, "stale": ERR}.get(sync, FAINT)
        g.rrect(16, y, W - 32, 52, 14, SURFACE)
        g.circle(36, y + 26, 7, col)
        g.text(title, 54, y + 22, 18, TEXT, 0, W - 86)
        g.text(detail, 54, y + 42, 15, DIM, 0, W - 86)
        y += 62
        if v.kind == "power":
            bw = (W - 32 - 10) // 2
            on_sel = v.on_known and v.on
            off_sel = v.on_known and not v.on
            self.button(16, y, bw, 58, "On", "power", True, "selected" if on_sel else "normal",
                        icon="lightbulb" if d.get("type") != "outlet" else "power", size=21)
            self.button(26 + bw, y, bw, 58, "Off", "power", False, "selected" if off_sel else "normal", icon="power", size=21)
            y += 70
            if d.get("has_brightness"):
                g.text("Brightness", 20, y + 14, 16, DIM)
                g.text("%d%%" % v.bri, W - 20, y + 14, 16, TEXT, 2)
                y += 20
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
                y += 64
                n = len(PRESETS)
                cw = (W - 32 - (n - 1) * 8) // n
                for i, p in enumerate(PRESETS):
                    self.button(16 + i * (cw + 8), y, cw, 42, "%d%%" % p, "bri", p,
                                "selected" if v.bri == p and v.on else "normal", size=17)
                y += 52
            timed_on = not (v.on_known and v.on)
            g.text("Turn %s for, then back" % ("on" if timed_on else "off"), 20, y + 14, 16, DIM)
            y += 22
            n = len(TIMERS)
            cw = (W - 32 - (n - 1) * 8) // n
            for i, s in enumerate(TIMERS):
                self.button(16 + i * (cw + 8), y, cw, 46, M.dur_label(s), "timer", s, icon="timer", size=17)
            y += 56
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
        if d.get("is_faulty") and y < H - 24:
            g.text("HomeKit reports a fault on this device.", 20, y + 14, 16, WARN)

    # ----- settings -----

    def panel_rows(self):
        app = self.app
        info = app.net_info()
        return (("Wi-Fi", info.get("ssid") or "—"), ("Signal", info.get("signal") or "—"),
                ("Address", info.get("ip") or "—"), ("Name", info.get("host") or "—"),
                ("Last frame", "%d ms" % self.render_ms), ("App", app.VERSION))

    def settings_screens(self):
        """Settings blocks grouped into screens that fit the grid area."""
        blocks = [("controller", 104), ("sort", 84), ("backlight", 92), ("dim", 84), ("ambient", 84), ("click", 84)]
        if self.app.settings.dirty():
            blocks.append(("pending", 96))
        blocks.append(("panel", 24 + 6 * 44))
        screens, cur, used = [], [], 0
        for name, h in blocks:
            if cur and used + h > GRID[3]:
                screens.append(cur)
                cur, used = [], 0
            cur.append((name, h))
            used += h
        screens.append(cur)
        return screens

    def draw_settings(self):
        g = self.g
        app = self.app
        s = app.settings
        screens = self.settings_screens()
        screen = self.screen_of("settings", len(screens))
        cy = BAR[1] + BAR[3] // 2
        left = self.pager(W - MARGIN, cy, screen, len(screens))
        g.text_mid("Settings", MARGIN + 4, cy, 19, DIM, 0, left - MARGIN - 16)
        y = GRID[1] + 4

        def label(text, y):
            g.text(text.upper(), MARGIN + 6, y + 16, 14, FAINT)

        for name, h in screens[screen]:
            if name == "controller":
                label("Controller", y)
                c = y + 24
                g.rrect(MARGIN, c, W - 2 * MARGIN, 72, 16, SURFACE)
                g.icon("dns", MARGIN + 34, c + 36, 38, BLUE)
                g.text(app.title(), MARGIN + 66, c + 31, 21, TEXT, 0, W - 220)
                g.text(app.client.label() if app.client else "—", MARGIN + 66, c + 56, 16, DIM, 0, W - 220)
                if (app.server or {}).get("fixed"):
                    g.text("set in secrets.py", W - MARGIN - 16, c + 42, 15, FAINT, 2)
                else:
                    self.button(W - MARGIN - 112, c + 12, 100, 48, "Change", "find", None, size=18)
            elif name == "sort":
                label("Light order", y)
                self.chips(MARGIN, y + 24, W - 2 * MARGIN, 48, SORT_CHOICES, "light_sort")
            elif name == "backlight":
                label("Screen brightness", y)
                sr = (MARGIN, y + 24, W - 2 * MARGIN, 56)
                self.add_hot(sr, "backlight", None, F_SLIDER)
                if g.visible(*sr):
                    g.rrect(sr[0], sr[1], sr[2], sr[3], 16, SURFACE)
                    pad = 22
                    tx, tw = sr[0] + pad, sr[2] - 2 * pad
                    k = tx + int(tw * (s["backlight"] - 0.1) / 0.9)
                    g.rrect(tx - 4, sr[1] + 22, tw + 8, 12, 6, SURFACE_2)
                    g.rrect(tx - 4, sr[1] + 22, k - tx + 8, 12, 6, BLUE)
                    g.circle(k, sr[1] + 28, 15, TEXT)
            elif name == "dim":
                label("Dim when idle", y)
                self.chips(MARGIN, y + 24, W - 2 * MARGIN, 48, DIM_CHOICES, "dim_after_s")
            elif name == "ambient":
                label("Back LEDs", y)
                self.chips(MARGIN, y + 24, W - 2 * MARGIN, 48, AMBIENT_CHOICES, "ambient")
            elif name == "click":
                label("Tap sound", y)
                self.chips(MARGIN, y + 24, W - 2 * MARGIN, 48, ((False, "Off"), (True, "On")), "click")
            elif name == "pending":
                label("Not yet stored", y)
                c = y + 24
                g.rrect(MARGIN, c, W - 2 * MARGIN, 64, 16, SURFACE)
                g.text("Active now; stored permanently at the", MARGIN + 16, c + 27, 15, DIM, 0, W - 190)
                g.text("next restart (automatic when idle).", MARGIN + 16, c + 48, 15, DIM, 0, W - 190)
                self.button(W - MARGIN - 140, c + 10, 128, 44, "Restart now", "restart", None, size=16)
            elif name == "panel":
                label("Panel", y)
                rows = self.panel_rows()
                self.drawn["panel"] = rows
                ry = y + 24
                for k, val in rows:
                    g.rect(MARGIN, ry, W - 2 * MARGIN, 43, SURFACE)
                    g.text_mid(k, MARGIN + 16, ry + 21, 17, DIM)
                    g.text_mid(val, W - MARGIN - 16, ry + 21, 17, TEXT, 2, W - 180)
                    ry += 44
            y += h

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
