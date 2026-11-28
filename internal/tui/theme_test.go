package tui

import "testing"

func TestPickBullet(t *testing.T) {
	for _, c := range []struct{ goos, term, ghostty, want string }{
		{"darwin", "Apple_Terminal", "", "⏺"},
		{"darwin", "iTerm.app", "", "⏺"},
		{"darwin", "ghostty", "/Applications/Ghostty.app/Contents/Resources/ghostty", "●"},
		{"darwin", "tmux", "/Applications/Ghostty.app/Contents/Resources/ghostty", "●"}, // Ghostty, inside tmux
		{"linux", "ghostty", "/usr/share/ghostty", "●"},
		{"linux", "kitty", "", "●"},
		{"linux", "", "", "●"},
		{"windows", "", "", "●"},
	} {
		if got := pickBullet(c.goos, c.term, c.ghostty); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
}
