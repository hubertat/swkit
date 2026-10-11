#!/bin/sh
# Headless run of the app on the MicroPython unix port (see mp_check.py).
set -e
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
cp "$here/../assets/swkit.af" "$work/"
printf 'WIFI_SSID = "HomeNet"\nWIFI_PASSWORD = "x"\nSWKIT_URL = "http://127.0.0.1:18081/control"\n' > "$work/secrets.py"
cd "$work"
MICROPYPATH=".frozen:$work:$here/mpfake:$here/../src" micropython "$here/mp_check.py"
