#!/usr/bin/env bash
# Removes what install.sh added. Your keys/settings in ~/.config/werkz are kept.
set -u
rm -f "$HOME/.local/bin/werkz" "$HOME/.local/bin/werkz-launch" "$HOME/.local/share/applications/werkz.desktop"
for s in 16 24 32 48 64 128 256 512; do rm -f "$HOME/.local/share/icons/hicolor/${s}x${s}/apps/werkz.png"; done
# Rebuild the icon cache, or it keeps pointing at the deleted icons (a broken icon if werkz is
# later installed again, for example from a .deb).
gtk-update-icon-cache -q -t "$HOME/.local/share/icons/hicolor" 2>/dev/null || true
DESK="$(xdg-user-dir DESKTOP 2>/dev/null || echo "$HOME/Desktop")"
rm -f "$DESK/werkz.desktop"
update-desktop-database -q "$HOME/.local/share/applications" 2>/dev/null || true
echo "✓ werkz removed (settings kept in ~/.config/werkz)"
