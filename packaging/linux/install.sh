#!/usr/bin/env bash
# Installs werkz for the current user: binary, launcher, icons, app-grid entry and a Desktop icon.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Unsupported CPU: $(uname -m)"; exit 1 ;;
esac

BIN_DIR="$HOME/.local/bin"
APP_DIR="$HOME/.local/share/applications"
ICON_DIR="$HOME/.local/share/icons/hicolor"
mkdir -p "$BIN_DIR" "$APP_DIR"

install -m 755 "$HERE/bin/werkz-linux-$ARCH" "$BIN_DIR/werkz"
install -m 755 "$HERE/werkz-launch" "$BIN_DIR/werkz-launch"

for s in 16 24 32 48 64 128 256 512; do
  mkdir -p "$ICON_DIR/${s}x${s}/apps"
  install -m 644 "$HERE/icons/werkz-$s.png" "$ICON_DIR/${s}x${s}/apps/werkz.png"
done

DESKTOP_FILE="$APP_DIR/werkz.desktop"
cat > "$DESKTOP_FILE" <<EOF
[Desktop Entry]
Type=Application
Name=werkz
GenericName=AI Coding Assistant
Comment=Claude + free fallback models in your terminal
Exec=$BIN_DIR/werkz-launch
Icon=werkz
Terminal=false
Categories=Development;
Keywords=ai;claude;gemini;terminal;coding;assistant;
StartupNotify=true
EOF
chmod 755 "$DESKTOP_FILE"

# Clickable icon on the Desktop
DESK="$(xdg-user-dir DESKTOP 2>/dev/null || echo "$HOME/Desktop")"
if [ -d "$DESK" ]; then
  install -m 755 "$DESKTOP_FILE" "$DESK/werkz.desktop"
  gio set "$DESK/werkz.desktop" metadata::trusted true 2>/dev/null || true
fi

gtk-update-icon-cache -q -t "$ICON_DIR" 2>/dev/null || true
update-desktop-database -q "$APP_DIR" 2>/dev/null || true

echo "✓ werkz installed"
echo "  • App grid: search for “werkz”"
echo "  • Desktop:  $DESK/werkz.desktop (if it shows as a file, right-click → Allow Launching)"
case ":$PATH:" in *":$BIN_DIR:"*) ;; *) echo "  • Add $BIN_DIR to PATH to run 'werkz' from any terminal";; esac
