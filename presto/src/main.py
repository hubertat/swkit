# ICON lightbulb
# NAME swkit
# DESC Control swkit lights, outlets and scenes.

# swkit panel entry point; the app lives in /swkit (see presto/README.md).
# The header above lets Pimoroni's launcher list it when installed as
# /swkit.py instead of /main.py (deploy.sh --launcher).
import sys

sys.path.insert(0, "/swkit")

import app  # noqa: E402

app.main()
