#!/bin/bash
# werkz.app entry point: opens werkz in a new Terminal (or iTerm) window.
APP_RES="$(cd "$(dirname "$0")/../Resources" && pwd)"
case "$(uname -m)" in
  arm64) BIN="$APP_RES/bin/werkz-darwin-arm64" ;;
  *)     BIN="$APP_RES/bin/werkz-darwin-amd64" ;;
esac
chmod +x "$BIN" 2>/dev/null
xattr -d com.apple.quarantine "$BIN" 2>/dev/null

CMD="clear; cd ~; '$BIN'; exit"

if [ "${WERKZ_TERMINAL:-}" = "iterm" ] && [ -d "/Applications/iTerm.app" ]; then
  osascript <<EOF
tell application "iTerm"
  activate
  create window with default profile command "bash -lc \"$CMD\""
end tell
EOF
  exit 0
fi

osascript <<EOF
tell application "Terminal"
  activate
  set t to do script "$CMD"
  try
    set current settings of t to settings set "Pro"
  end try
  try
    set number of columns of t to 130
    set number of rows of t to 40
  end try
  try
    set custom title of t to "werkz"
  end try
end tell
EOF
