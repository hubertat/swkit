"""CPython stand-in for MicroPython's network module (simulator only)."""

STA_IF = 0
_hostname = "PPP2"


def hostname(name=None):
    global _hostname
    if name is None:
        return _hostname
    _hostname = name


class WLAN:
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

    def config(self, key):
        return {"ssid": "HomeNet", "mac": b"\x28\xcd\xc1\x00\x00\x01"}.get(key)

    def ifconfig(self):
        return ("192.168.1.77", "255.255.255.0", "192.168.1.1", "192.168.1.1")
