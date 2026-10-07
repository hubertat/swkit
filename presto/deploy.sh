#!/bin/sh
# Copies the swkit panel onto a Presto over USB with mpremote.
#
#   pip install mpremote
#   ./deploy.sh                 # install as /main.py (starts on boot)
#   ./deploy.sh --launcher      # install as /swkit.py, keep Pimoroni's launcher
#   ./deploy.sh --mpy           # precompile modules with mpy-cross first
#   PORT=/dev/ttyACM0 ./deploy.sh
#   DRY_RUN=1 ./deploy.sh       # print the mpremote command only
#
# Files land as: /swkit/*.py and /swkit/swkit.af (the app and its font),
# /main.py or /swkit.py (entry point) and /secrets.py (only if
# src/secrets.py exists; an existing one on the device is left alone).
set -e
cd "$(dirname "$0")"

entry=main.py
mpy=0
for arg in "$@"; do
    case "$arg" in
        --launcher) entry=swkit.py ;;
        --mpy) mpy=1 ;;
        -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
        *) echo "unknown option: $arg" >&2; exit 2 ;;
    esac
done

command -v mpremote >/dev/null || { echo "mpremote not found: pip install mpremote" >&2; exit 1; }
[ -f assets/swkit.af ] || { echo "assets/swkit.af missing: python3 tools/make_font.py" >&2; exit 1; }

mods=""
for f in src/*.py; do
    case "$(basename "$f")" in
        main.py|secrets.py|secrets.example.py) ;;
        *) mods="$mods $f" ;;
    esac
done

if [ "$mpy" = 1 ]; then
    command -v mpy-cross >/dev/null || { echo "mpy-cross not found: pip install mpy-cross" >&2; exit 1; }
    rm -rf build/mpy && mkdir -p build/mpy
    for f in $mods; do
        mpy-cross -o "build/mpy/$(basename "$f" .py).mpy" "$f"
    done
    mods=$(ls build/mpy/*.mpy)
fi

conn=""
[ -n "$PORT" ] && conn="connect $PORT"

# One mpremote session: create /swkit, clear the previous install there (it
# is this app's own directory), copy, reset.
cmd="exec \"import os
try:
    os.mkdir('/swkit')
except OSError:
    pass
for n in os.listdir('/swkit'):
    os.remove('/swkit/' + n)
\""
for f in $mods; do
    cmd="$cmd + cp $f :/swkit/$(basename "$f")"
done
cmd="$cmd + cp assets/swkit.af :/swkit/swkit.af + cp src/main.py :/$entry"
if [ -f src/secrets.py ]; then
    cmd="$cmd + cp src/secrets.py :/secrets.py"
fi
cmd="$cmd + reset"

if [ -n "$DRY_RUN" ]; then
    echo "mpremote $conn $cmd"
    exit 0
fi
eval "mpremote $conn $cmd"
