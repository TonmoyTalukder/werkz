package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/llm"
)

func TestSaveListLoad(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	s := &Session{Meta: Meta{ID: NewID(), Created: time.Now(), Updated: time.Now(), Dirs: []string{dir}},
		Items: []Item{{Kind: "user", Text: "fix the   login\nform"}, {Kind: "assistant", Text: "Done: empty passwords are rejected."}},
		History: FromHistory([]llm.Msg{{Role: llm.RoleUser, Text: "fix the login form"},
			{Role: llm.RoleAssistant, Text: "ok", ToolCalls: []llm.ToolCall{{ID: "1", Name: "Read", Args: `{"path":"a"}`}}}})}
	if err := Save(s, 1); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(Dir(), s.ID+".json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", st.Mode().Perm(), err)
	}
	list, _ := List()
	if len(list) != 1 || list[0].Title != "fix the login form" || list[0].Prompts != 1 {
		t.Fatalf("list = %+v", list)
	}
	got, err := Load(s.ID)
	if err != nil || len(got.Items) != 2 || ToHistory(got.History)[1].ToolCalls[0].Name != "Read" {
		t.Fatalf("load = %+v, %v", got, err)
	}
	if ok, ex := list[0].Match("PASSWORDS rejected"); !ok || ex == "" {
		t.Fatalf("match in answer = %v %q", ok, ex)
	}
	if ok, ex := list[0].Match("login"); !ok || ex != "" {
		t.Fatalf("match in title = %v %q", ok, ex)
	}
	if ok, _ := list[0].Match("logout"); ok {
		t.Fatal("matched a word that isn't there")
	}
	if m, err := Latest(dir); err != nil || m.ID != s.ID {
		t.Fatalf("latest = %v, %v", m, err)
	}
	// an older save finishing late doesn't overwrite a newer one
	s.Items = append(s.Items, Item{Kind: "user", Text: "and add a test"})
	_ = Save(s, 3)
	s.Items = s.Items[:2]
	_ = Save(s, 2)
	if got, _ := Load(s.ID); len(got.Items) != 3 {
		t.Fatalf("stale save won: %d items", len(got.Items))
	}
	// conversations without a request aren't saved
	empty := &Session{Meta: Meta{ID: NewID()}}
	_ = Save(empty, 1)
	if _, err := Load(empty.ID); err == nil {
		t.Fatal("empty conversation saved")
	}
	if err := Delete(s.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(); len(list) != 0 {
		t.Fatalf("after delete: %d", len(list))
	}
	if _, err := Load("../config"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
