package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/config"
)

func TestAttachMentions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, other := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "src/a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "bin.dat"), []byte{1, 0, 2}, 0o644)
	_ = os.WriteFile(filepath.Join(other, "secret.txt"), []byte("nope\n"), 0o644)
	e := New(config.Load(), func(any) {})
	e.SetWorkspace([]string{root}, nil, "")

	text, items := e.attachMentions("check @src/a.go:3 and @src/ and @bin.dat and @" + filepath.Join(other, "secret.txt") + " and @nothing.go and a@b.com")
	if len(items) != 4 {
		t.Fatalf("items = %+v", items)
	}
	if !strings.Contains(text, "func A() {}") || strings.Contains(text, "package a") {
		t.Fatalf("line range not respected:\n%s", text)
	}
	if !strings.Contains(text, `<folder path="src">`) || !strings.Contains(text, "binary file") {
		t.Fatalf("folder/binary missing:\n%s", text)
	}
	if strings.Contains(text, "nope") || items[3].OK {
		t.Fatalf("a file outside the folders was attached: %+v", items[3])
	}
	if err := e.reads.Check(filepath.Join(root, "src/a.go")); err != nil {
		t.Fatalf("attached file not marked as read: %v", err)
	}
	// the same unchanged file isn't sent twice
	text, _ = e.attachMentions("again @src/a.go:3")
	if !strings.Contains(text, "unchanged since it was attached") {
		t.Fatalf("re-attached unchanged file:\n%s", text)
	}
}
