#!/bin/bash
# werkz.app entry point: opens werkz in a new Terminal (or iTerm) window.
# The binary path is passed to AppleScript as an argument and quoted there with
# "quoted form of", so unusual characters in the folder name can't break or inject
# into the command.
APP_RES="$(cd "$(dirname "$0")/../Resources" && pwd)"
case "$(uname -m)" in
  arm64) BIN="$APP_RES/bin/werkz-darwin-arm64" ;;
  *)     BIN="$APP_RES/bin/werkz-darwin-amd64" ;;
esac

if [ "${WERKZ_TERMINAL:-}" = "iterm" ] && [ -d "/Applications/iTerm.app" ]; then
  osascript - "$BIN" <<'APPLESCRIPT'
on run argv
  set cmd to "clear; cd ~; " & quoted form of (item 1 of argv) & "; exit"
  tell application "iTerm"
    activate
    create window with default profile command "/bin/bash -lc " & quoted form of cmd
  end tell
end run
APPLESCRIPT
  exit 0
fi

osascript - "$BIN" <<'APPLESCRIPT'
on run argv
  set cmd to "clear; cd ~; " & quoted form of (item 1 of argv) & "; exit"
  tell application "Terminal"
    activate
    set t to do script cmd
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
end run
APPLESCRIPT
