"""CPython stand-in for MicroPython's network module (simulator only)."""

STA_IF = 0
_hostname = "PPP2"


def hostname(name=None):
    global _hostname
    if name is None:
        return _hostname
    _hostname = name


class WLAN:
    PM_NONE = 0x10
    PM_PERFORMANCE = 0xa11142
    PM_POWERSAVE = 0x11
    pm = PM_PERFORMANCE         # class-wide: every WLAN() is the same radio

    def __init__(self, iface=STA_IF):
        pass

    def active(self, *a):
        return True

    def isconnected(self):
        return True

    def status(self, key=None):
        if key == "rssi":
            return -58
        return 3

    def config(self, key=None, pm=None):
        if pm is not None:
            WLAN.pm = pm
            return None
        if key == "pm":
            return WLAN.pm
        return {"ssid": "HomeNet", "mac": b"\x28\xcd\xc1\x00\x00\x01"}.get(key)

    def ifconfig(self):
        return ("192.168.1.77", "255.255.255.0", "192.168.1.1", "192.168.1.1")
