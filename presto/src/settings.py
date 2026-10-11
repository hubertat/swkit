# Persistent panel settings, stored as JSON on the Presto's flash.
#
# Wi-Fi credentials live in secrets.py (the Presto convention, see
# secrets.example.py). Everything here can be changed on the device itself,
# from the Settings page.

import json

FILE = "swkit_settings.json"

DEFAULTS = {
    # Controller to use: {"name", "host", "port", "path"}; None = discover.
    "server": None,
    "backlight": 0.8,       # 0.1 .. 1.0
    "dim_after_s": 120,     # 0 = never dim
    "dim_level": 0.08,
    "home_after_s": 300,    # go back to the first page after this idle time; 0 = never
    "ambient": "status",    # "off" | "status"
    "click": False,         # buzzer tick on tap
    "page": 0,
}


class Settings:
    def __init__(self, path=FILE):
        self.path = path
        self.data = dict(DEFAULTS)
        try:
            with open(path) as f:
                saved = json.load(f)
            if isinstance(saved, dict):
                for k, v in saved.items():
                    if k in DEFAULTS:
                        self.data[k] = v
        except (OSError, ValueError):
            pass

    def __getitem__(self, key):
        return self.data[key]

    def set(self, key, value, save=True):
        if self.data.get(key) == value:
            return
        self.data[key] = value
        if save:
            self.save()

    def save(self):
        try:
            with open(self.path, "w") as f:
                json.dump(self.data, f)
        except OSError as e:
            print("settings: save failed:", e)
