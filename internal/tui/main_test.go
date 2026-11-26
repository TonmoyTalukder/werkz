package tui

import (
	"os"
	"testing"
)

// Tests must not depend on what is installed on the machine running them.
func TestMain(m *testing.M) {
	os.Setenv("WERKZ_CLAUDE_BIN", "off")
	os.Exit(m.Run())
}
