# Tests for src/settings.py: runtime saves stay in RAM, boot commits them.
# Runs on CPython and the MicroPython unix port (from presto/):
#   python3 sim/test_settings.py
#   MICROPYPATH=.frozen:src micropython sim/test_settings.py
import os
import sys

sys.path.insert(0, "src")
import settings as S  # noqa: E402

TMP = "sim_settings_tmp"
FLASH = TMP + "/flash.json"
RAM = TMP + "/ram"


def clean():
    for d in (RAM, TMP):
        try:
            for n in os.listdir(d):
                try:
                    os.remove(d + "/" + n)
                except OSError:
                    pass
            os.rmdir(d)
        except OSError:
            pass


def read(path):
    try:
        with open(path) as f:
            return f.read()
    except OSError:
        return None


def test_runtime_save_never_writes_flash():
    s = S.Settings(FLASH, RAM)
    s.set("ambient", "off")
    s.set("dim_after_s", 30)
    assert read(FLASH) is None, "flash written at runtime"
    assert s.dirty()


def test_boot_commit_copies_ram_to_flash():
    s = S.Settings(FLASH, RAM)          # next boot: RAM copy wins
    assert s["ambient"] == "off" and s["dim_after_s"] == 30
    assert s.dirty() and s.commit() and not s.dirty()
    assert S.Settings(FLASH)["ambient"] == "off", "flash not updated"
    assert S.Settings(FLASH, RAM).dirty() is False


def test_unchanged_value_does_not_dirty():
    s = S.Settings(FLASH, RAM)
    s.set("ambient", "off")
    assert not s.dirty()


def test_unknown_keys_dropped_defaults_filled():
    with open(FLASH, "w") as f:
        f.write('{"bogus": 1, "backlight": 0.5}')
    s = S.Settings(FLASH, None)
    assert s["backlight"] == 0.5 and s["light_sort"] == "config"
    assert "bogus" not in s.data


def test_without_ramdisk_saves_directly():
    s = S.Settings(FLASH, None)
    s.set("light_sort", "name")
    assert not s.dirty()
    assert S.Settings(FLASH)["light_sort"] == "name"


def test_memory_only_never_writes():
    before = read(FLASH)
    s = S.Settings(FLASH, S.MEMORY_ONLY)
    s.set("light_sort", "on_first")
    assert s["light_sort"] == "on_first" and read(FLASH) == before


def run():
    clean()
    os.mkdir(TMP)
    os.mkdir(RAM)
    failed = 0
    # In order: each step continues from the state the previous one left.
    tests = [(f.__name__, f) for f in (test_runtime_save_never_writes_flash, test_boot_commit_copies_ram_to_flash,
                                        test_unchanged_value_does_not_dirty, test_unknown_keys_dropped_defaults_filled,
                                        test_without_ramdisk_saves_directly, test_memory_only_never_writes)]
    for name, fn in tests:
        try:
            fn()
            print("ok  ", name)
        except Exception as e:  # noqa: BLE001
            failed += 1
            print("FAIL", name, repr(e))
    clean()
    print("%d passed, %d failed" % (len(tests) - failed, failed))
    sys.exit(1 if failed else 0)


run()
