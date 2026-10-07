STA_IF = 0


def hostname(name=None):
    return "swkit-panel"


class WLAN:
    def __init__(self, i=0):
        pass

    def status(self, key=None):
        return -60 if key == "rssi" else 3

    def config(self, key):
        return "HomeNet"
