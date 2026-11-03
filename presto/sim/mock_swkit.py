#!/usr/bin/env python3
"""A stand-in for swkit's control API, for developing the Presto UI on a PC.

It mimics what makes the real thing tricky: commands are acknowledged at
once but the device only "reports" the new state after a delay (like Shelly
over MQTT or a Wago poll), one device can't be read, one is offline, and
buttons get pressed now and then.

    python3 sim/mock_swkit.py --port 8080
"""

import argparse
import json
import random
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCK = threading.Lock()
REPORT_DELAY = (0.6, 1.6)
TOGGLE_TIMEOUT = 20   # seconds idle before a scene's next toggle returns to state 0

DEVICES = [
    {"name": "Kitchen", "type": "light"},
    {"name": "Living room", "type": "dimmable_light", "brightness": 60, "is_on": True},
    {"name": "Hallway", "type": "light", "is_on": True},
    {"name": "Bedroom lamp", "type": "dimmable_light", "brightness": 30},
    {"name": "Terrace", "type": "light", "state_error": "shelly: device status stale"},
    {"name": "Bathroom mirror", "type": "light"},
    {"name": "Kids room ceiling", "type": "dimmable_light", "brightness": 80},
    {"name": "Garage", "type": "light", "is_healthy": False},
    {"name": "Office desk", "type": "light"},
    {"name": "Coffee machine", "type": "outlet", "is_on": True},
    {"name": "Fan", "type": "outlet"},
    {"name": "Evening", "type": "scene", "scene_states": ["Off", "Dim", "Bright"]},
    {"name": "Movie", "type": "scene", "scene_states": ["Off", "On"]},
    {"name": "Hall switch", "type": "button"},
    {"name": "Bedside left", "type": "button"},
    {"name": "Doorbell", "type": "button"},
    # Window shutters (appended so the other indices stay put). Travel is
    # simulated from time like the real controller's estimate.
    {"name": "Bedroom shutter", "type": "window_shutter", "travel": 12},
    {"name": "Living room shutter", "type": "window_shutter", "travel": 18, "pos": 100, "known": True},
]
OVERRUN = 0.10


def init():
    for i, d in enumerate(DEVICES):
        d.setdefault("is_on", False)
        d.setdefault("is_healthy", True)
        d.setdefault("brightness", 0)
        d["index"] = i
        d["scene_index"] = 0       # selected state (published at toggle time)
        d["changed_at"] = time.time()
        d["last_event_time"] = None
        if d["type"] == "window_shutter":
            d.setdefault("pos", 0.0)
            d.setdefault("known", False)
            d.update(motion="stopped", target=-1, start_t=0, start_pos=0, stop_at=0, last_dir=None)


def sh_pos(d, now):
    """Estimated position (percent open), advancing while moving."""
    if d["motion"] == "stopped":
        return d["pos"]
    delta = 100.0 * (now - d["start_t"]) / d["travel"]
    p = d["start_pos"] + (delta if d["motion"] == "opening" else -delta)
    return max(0.0, min(100.0, p))


def sh_settle(d, now):
    if d["motion"] != "stopped" and now >= d["stop_at"]:
        d["pos"] = float(d["target"])
        if d["target"] in (0, 100):
            d["known"] = True
        d["motion"] = "stopped"


def sh_stop(d, now):
    if d["motion"] != "stopped":
        d["pos"] = sh_pos(d, now) if d["known"] else d["pos"]
        d["motion"] = "stopped"
        d["target"] = int(round(d["pos"])) if d["known"] else -1


def sh_start(d, target, now):
    if not d["known"] and target not in (0, 100):
        raise ValueError("window shutter %s: position is unknown; run a full open or close first" % d["name"])
    sh_stop(d, now)
    d["motion"] = "opening" if (target == 100 if not d["known"] else target > d["pos"]) else "closing"
    dist = abs(target - d["pos"]) if d["known"] else 100
    run = d["travel"] * dist / 100.0 + (d["travel"] * OVERRUN if target in (0, 100) else 0)
    d.update(target=target, start_t=now, start_pos=d["pos"], stop_at=now + run, last_dir=d["motion"])


def sh_state(d, now):
    sh_settle(d, now)
    return {
        "motion": d["motion"], "target_position": d["target"],
        "current_position": int(round(sh_pos(d, now))) if d["known"] else 0,
        "position_known": d["known"], "pending_start": False,
        "open_io_id": "mock|d_out|%d0" % d["index"], "close_io_id": "mock|d_out|%d1" % d["index"],
        "open_duration_seconds": d["travel"], "close_duration_seconds": d["travel"],
        "remaining_travel_ms": max(0, int((d["stop_at"] - now) * 1000)) if d["motion"] != "stopped" else 0,
    }


def later(fn):
    threading.Timer(random.uniform(*REPORT_DELAY), fn).start()


def next_state(d):
    """What a toggle would select now: state 0 after TOGGLE_TIMEOUT idle seconds."""
    n = len(d["scene_states"])
    if d["scene_index"] != 0 and time.time() - d["changed_at"] >= TOGGLE_TIMEOUT:
        return 0
    return (d["scene_index"] + 1) % n


def snapshot():
    out = []
    now = time.time()
    for d in DEVICES:
        t = d["type"]
        r = {
            "index": d["index"], "name": d["name"], "type": t,
            "is_on": d["is_on"] and not d.get("state_error"),
            "is_healthy": d["is_healthy"], "is_faulty": False,
            "controllable": t != "button", "has_brightness": t == "dimmable_light",
            "brightness": d["brightness"],
        }
        if d.get("state_error"):
            r["state_error"] = d["state_error"]
        if t == "scene":
            r["scene_index"] = d["scene_index"]
            r["scene_count"] = len(d["scene_states"])
            r["scene_state"] = d["scene_states"][d["scene_index"]]
            r["scene_next"] = next_state(d)
        if t == "window_shutter":
            ws = sh_state(d, now)
            r["window_shutter"] = ws
            r["is_on"] = ws["position_known"] and ws["current_position"] > 0
        if t == "button" and d["last_event_time"]:
            r["last_event_type"] = d["last_event_type"]
            r["last_event_age_ms"] = max(1, int((now - d["last_event_time"]) * 1000))
        out.append(r)
    return out


def press_buttons():
    while True:
        time.sleep(random.uniform(6, 20))
        with LOCK:
            b = random.choice([d for d in DEVICES if d["type"] == "button"])
            b["last_event_type"] = random.choice(["single_press", "single_press", "double_press", "long_press"])
            b["last_event_time"] = time.time()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        if not self.path.endswith("/api/devices"):
            super().log_message(fmt, *args)

    def reply(self, code, obj=None, text=None):
        body = (json.dumps(obj) if obj is not None else (text or "")).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json" if obj is not None else "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/control/api/info":
            return self.reply(200, {"name": "Mock home", "api": 1, "features": ["window_shutter"]})
        if self.path == "/control/api/devices":
            with LOCK:
                return self.reply(200, snapshot())
        self.reply(404, text="not found")

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n) or b"{}")
        parts = self.path.split("/")
        if len(parts) != 6 or parts[1:4] != ["control", "api", "devices"]:
            return self.reply(404, text="not found")
        try:
            d = DEVICES[int(parts[4])]
        except (ValueError, IndexError):
            return self.reply(500, text="device index out of range")
        action = parts[5]
        time.sleep(random.uniform(0.05, 0.25))
        if not d["is_healthy"]:
            return self.reply(500, text="device not healthy")
        with LOCK:
            if d["type"] == "window_shutter":
                now = time.time()
                sh_settle(d, now)
                try:
                    if action == "stop":
                        sh_stop(d, now)
                    elif action == "open" or (action == "set" and body.get("value")):
                        sh_start(d, 100, now)
                    elif action == "close" or action == "set":
                        sh_start(d, 0, now)
                    elif action == "set_position":
                        sh_start(d, max(0, min(100, int(body.get("value", 0)))), now)
                    elif action == "toggle":
                        if d["motion"] != "stopped":
                            sh_stop(d, now)
                        else:
                            sh_start(d, 0 if d["last_dir"] == "opening" else 100, now)
                    else:
                        return self.reply(404, text="not found")
                except ValueError as e:
                    return self.reply(409, text=str(e))
                ws = sh_state(d, now)
                return self.reply(200, {"device_name": d["name"], "action": action,
                                        "is_on": ws["position_known"] and ws["current_position"] > 0, "window_shutter": ws})
            if action == "toggle":
                if d["type"] == "scene":
                    # Selected (and published) now; the lights follow after the delay.
                    nxt = next_state(d)
                    d.update(scene_index=nxt, changed_at=time.time())
                    later(lambda: d.update(is_on=nxt != 0))
                    return self.reply(200, {"device_name": d["name"], "action": action, "is_on": nxt != 0, "scene_index": nxt})
                else:
                    v = not d["is_on"]
                    later(lambda: d.update(is_on=v))
            elif action in ("set", "set_for"):
                v = bool(body.get("value"))
                later(lambda: d.update(is_on=v))
                if action == "set_for":
                    prev = d["is_on"]
                    threading.Timer(int(body.get("seconds", 0)), lambda: d.update(is_on=prev)).start()
            elif action == "set_brightness":
                v = max(0, min(100, int(body.get("value", 0))))
                later(lambda: d.update(brightness=v, is_on=v > 0))
            else:
                return self.reply(404, text="not found")
        self.reply(200, {"device_name": d["name"], "action": action, "is_on": d["is_on"]})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8080)
    ap.add_argument("--no-buttons", action="store_true", help="don't simulate button presses")
    args = ap.parse_args()
    init()
    if not args.no_buttons:
        threading.Thread(target=press_buttons, daemon=True).start()
    print("mock swkit on http://127.0.0.1:%d/control" % args.port)
    ThreadingHTTPServer(("0.0.0.0", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
