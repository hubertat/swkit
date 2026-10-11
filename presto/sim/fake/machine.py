"""CPython stand-in for MicroPython's machine module (simulator only)."""


class Pin:
    def __init__(self, *a, **k):
        pass


class PWM:
    def __init__(self, *a, **k):
        pass

    def freq(self, *a):
        pass

    def duty_u16(self, *a):
        pass


def reset():
    raise SystemExit("machine.reset()")
