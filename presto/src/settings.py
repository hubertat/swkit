# Persistent panel settings, stored as JSON.
#
# Wi-Fi credentials live in secrets.py (the Presto convention, see
# secrets.example.py). Everything here can be changed on the device itself,
# from the Settings page.
#
# Never write flash while the display runs. On Presto firmware v2.0.0 a flash
# write from Python hangs the device for good once Presto() has started core1:
# the firmware replaces pico-sdk's core1 lockout handler with one written for
# the old SDK protocol (it answers only the fixed request 0x73a8831e), while
# the bundled SDK 2.3 sends an incrementing request id and waits for its echo
# with no timeout.
#
# So runtime saves go to /ramfs, the PSRAM-backed RAM disk that survives
# machine.reset() (Pimoroni's launcher uses it the same way), and are copied
# to flash only by commit(), which must run at boot before Presto() exists.
# The app restarts quietly when idle to get pending changes committed.

import json

FILE = "swkit_settings.json"
RAM_DIR = "/ramfs"

DEFAULTS = {
    # Controller to use: {"name", "host", "port", "path"}; None = discover.
    "server": None,
    "backlight": 0.8,       # 0.1 .. 1.0
    "dim_after_s": 120,     # 0 = never dim
    "dim_level": 0.08,
    "home_after_s": 300,    # go back to the first page after this idle time; 0 = never
    "ambient": "status",    # "off" | "status" | "glow"
    "click": False,         # buzzer tick on tap
    "light_sort": "config", # "config" (swkit's config order) | "on_first" | "name"
}


def _read(path):
    try:
        with open(path) as f:
            data = json.load(f)
        return data if isinstance(data, dict) else None
    except (OSError, ValueError):
        return None


def _write(path, data):
    with open(path, "w") as f:
        f.write(json.dumps(data))


MEMORY_ONLY = ""   # mount_ramfs() result: on a Presto, but no RAM disk


def mount_ramfs():
    """Mounts the PSRAM RAM disk and returns its path. Returns None off the
    Presto (PC simulator: plain file writes are fine there) and MEMORY_ONLY
    when the RAM disk can't be used, so nothing is ever written to flash
    at runtime."""
    try:
        import psram
    except ImportError:
        return None
    import os
    try:
        psram.mkramfs(mount_point=RAM_DIR)
        return RAM_DIR
    except OSError as e:
        # Already mounted, e.g. by Pimoroni's launcher (deploy.sh --launcher).
        try:
            os.listdir(RAM_DIR)
            return RAM_DIR
        except OSError:
            print("settings: no RAM disk (%s); changes last until restart" % e)
            return MEMORY_ONLY


class Settings:
    def __init__(self, path=FILE, ram_dir=None):
        self.path = path
        self.memory_only = ram_dir == MEMORY_ONLY
        self.ram_path = (ram_dir.rstrip("/") + "/" + FILE) if ram_dir else None
        flash = _read(path) or {}
        self.flash = self._clean(flash)
        # A RAM copy is newer: it holds changes made since the last commit.
        ram = _read(self.ram_path) if self.ram_path else None
        self.data = self._clean(ram if ram is not None else flash)

    @staticmethod
    def _clean(saved):
        data = dict(DEFAULTS)
        for k, v in saved.items():
            if k in DEFAULTS:
                data[k] = v
        return data

    def __getitem__(self, key):
        return self.data[key]

    def set(self, key, value, save=True):
        if self.data.get(key) == value:
            return
        self.data[key] = value
        if save:
            self.save()

    def save(self):
        """Keeps the current settings: in RAM on the Presto (safe at any
        time), straight to the file where there is no RAM disk (PC sim)."""
        if self.memory_only:
            return
        try:
            _write(self.ram_path or self.path, self.data)
        except OSError as e:
            print("settings: save failed:", e)
            return
        if not self.ram_path:
            self.flash = dict(self.data)

    def dirty(self):
        """True when flash holds older settings than the panel uses."""
        return self.data != self.flash

    def commit(self):
        """Writes pending settings to flash. Only call before Presto() is
        created (see the module comment). Returns True if flash is current."""
        if not self.dirty():
            return True
        try:
            _write(self.path, self.data)
        except OSError as e:
            print("settings: commit failed:", e)
            return False
        self.flash = dict(self.data)
        return True
