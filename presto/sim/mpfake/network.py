STA_IF = 0


def hostname(name=None):
    return "swkit-panel"


class WLAN:
    PM_NONE = 0x10
    PM_PERFORMANCE = 0xa11142
    PM_POWERSAVE = 0x11
    pm = PM_PERFORMANCE         # class-wide: every WLAN() is the same radio

    def __init__(self, i=0):
        pass

    def status(self, key=None):
        return -60 if key == "rssi" else 3

    def config(self, key=None, pm=None):
        if pm is not None:
            WLAN.pm = pm
            return None
        if key == "pm":
            return WLAN.pm
        return "HomeNet"
