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
]


def init():
    for i, d in enumerate(DEVICES):
        d.setdefault("is_on", False)
        d.setdefault("is_healthy", True)
        d.setdefault("brightness", 0)
        d["index"] = i
        d["scene_index"] = 0
        d["last_event_time"] = None


def later(fn):
    threading.Timer(random.uniform(*REPORT_DELAY), fn).start()


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
            return self.reply(200, {"name": "Mock home", "api": 1})
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
            if action == "toggle":
                if d["type"] == "scene":
                    nxt = (d["scene_index"] + 1) % len(d["scene_states"])
                    later(lambda: d.update(scene_index=nxt, is_on=nxt != 0))
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
