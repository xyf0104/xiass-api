#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
  echo "This installer is for macOS." >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CONFIG_DIR="$HOME/Library/Application Support/XIASS/adspower-helper"
CONFIG_PATH="$CONFIG_DIR/config.json"
BIN_DIR="$CONFIG_DIR/bin"
BIN_PATH="$BIN_DIR/xiass-adspower-helper"
LOG_DIR="$HOME/Library/Logs/XIASS"
PLIST_PATH="$HOME/Library/LaunchAgents/com.xiass.adspower-helper.plist"
LABEL="com.xiass.adspower-helper"

if [ ! -f "$CONFIG_PATH" ]; then
  echo "Create $CONFIG_PATH before installing the helper." >&2
  exit 1
fi

mkdir -p "$BIN_DIR" "$LOG_DIR" "$HOME/Library/LaunchAgents"
chmod 700 "$CONFIG_DIR" "$BIN_DIR"
chmod 600 "$CONFIG_PATH"

TMP_BIN="$BIN_PATH.tmp"
(
  cd "$SCRIPT_DIR"
  go build -trimpath -ldflags "-s -w" -o "$TMP_BIN" .
)
chmod 700 "$TMP_BIN"
mv "$TMP_BIN" "$BIN_PATH"

TMP_PLIST="$PLIST_PATH.tmp"
export LABEL BIN_PATH CONFIG_PATH LOG_DIR TMP_PLIST
python3 - <<'PY'
import os
import plistlib

payload = {
    "Label": os.environ["LABEL"],
    "ProgramArguments": [
        "/usr/bin/caffeinate",
        "-i",
        os.environ["BIN_PATH"],
        "-config",
        os.environ["CONFIG_PATH"],
        "serve",
    ],
    "RunAtLoad": True,
    "KeepAlive": True,
    "ProcessType": "Interactive",
    "StandardOutPath": os.path.join(os.environ["LOG_DIR"], "adspower-helper.log"),
    "StandardErrorPath": os.path.join(os.environ["LOG_DIR"], "adspower-helper.log"),
}
with open(os.environ["TMP_PLIST"], "wb") as handle:
    plistlib.dump(payload, handle, sort_keys=False)
PY
plutil -lint "$TMP_PLIST" >/dev/null
chmod 600 "$TMP_PLIST"
mv "$TMP_PLIST" "$PLIST_PATH"

launchctl bootout "gui/$(id -u)/$LABEL" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$(id -u)" "$PLIST_PATH"
launchctl kickstart -k "gui/$(id -u)/$LABEL"

echo "Installed XIASS AdsPower Helper at $BIN_PATH"
