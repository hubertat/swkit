# Small shims so the same modules run on MicroPython and on CPython (sim/).

try:
    from time import ticks_ms, ticks_diff, ticks_add
except ImportError:
    import time as _t

    def ticks_ms():
        return int(_t.monotonic() * 1000)

    def ticks_diff(a, b):
        return a - b

    def ticks_add(a, b):
        return a + b

try:
    from gc import collect as gc_collect
except ImportError:
    def gc_collect():
        pass
