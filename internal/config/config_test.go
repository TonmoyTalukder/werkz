package config

import "testing"

// M4: auto and accept-edits modes are not remembered across sessions.
func TestPermissionModeNotPersisted(t *testing.T) {
	for mode, want := range map[string]string{ModeAuto: ModeDefault, ModeAcceptEdits: ModeDefault, ModePlan: ModePlan, ModeDefault: ModeDefault} {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		c := Load()
		c.Mode = mode
		if err := c.Save(); err != nil {
			t.Fatal(err)
		}
		if got := Load().Mode; got != want {
			t.Errorf("saved %s, next session starts in %s, want %s", mode, got, want)
		}
	}
}
