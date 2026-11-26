// Package session saves conversations so they can be found and continued later (/resume,
// werkz --continue). Each conversation is two files in the werkz config folder's sessions/
// directory: <id>.json holds everything needed to continue it, <id>.meta.json a small summary
// with the searchable text, so listing and searching stay fast. Files are private (0600).
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// Keep is how many conversations are kept; older ones are removed when a new one is saved.
const Keep = 500

const maxSearchText = 64 << 10

// Item is one thing the transcript showed, so a resumed conversation looks as it did.
type Item struct {
	Kind    string   `json:"k"` // user | assistant | tool | attach | note
	Text    string   `json:"t,omitempty"`
	Call    string   `json:"call,omitempty"` // tool: name
	Args    string   `json:"args,omitempty"` // tool: what it was called with (summary)
	Detail  []string `json:"d,omitempty"`    // tool: diff or output lines shown
	IsError bool     `json:"err,omitempty"`
	Image   string   `json:"img,omitempty"` // tool: a screenshot shown with the result
	OK      []bool   `json:"ok,omitempty"`  // attach: per line, attached or not
}

// Msg is a conversation message as saved: provider-neutral, without provider-specific
// replay data (thinking signatures, encrypted reasoning), which doesn't survive a restart.
type Msg struct {
	Role       llm.Role       `json:"role"`
	Text       string         `json:"text,omitempty"`
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
}

// Meta describes a saved conversation.
type Meta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"` // the first request
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Dirs      []string  `json:"dirs"`
	Workspace string    `json:"workspace,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Prompts   int       `json:"prompts"`
	Text      string    `json:"text,omitempty"` // requests and answers, for searching
}

// Session is a saved conversation.
type Session struct {
	Meta
	Items   []Item `json:"items"`
	History []Msg  `json:"history"`
}

// NewID returns a new conversation ID: sortable by time, with a random suffix.
func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Dir is where conversations are saved.
func Dir() string { return filepath.Join(filepath.Dir(config.Path()), "sessions") }

func validID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\.`) && len(id) < 100
}

// FromHistory converts the engine's conversation for saving.
func FromHistory(h []llm.Msg) []Msg {
	out := make([]Msg, len(h))
	for i, m := range h {
		out[i] = Msg{Role: m.Role, Text: m.Text, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID, ToolName: m.ToolName, IsError: m.IsError}
	}
	return out
}

// ToHistory converts a saved conversation back for the engine.
func ToHistory(h []Msg) []llm.Msg {
	out := make([]llm.Msg, len(h))
	for i, m := range h {
		out[i] = llm.Msg{Role: m.Role, Text: m.Text, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID, ToolName: m.ToolName, IsError: m.IsError}
	}
	return out
}

var (
	mu   sync.Mutex
	seqs = map[string]int64{} // newest save per conversation (saves may finish out of order)
)

// Save writes s (seq orders saves of the same conversation: an older one never overwrites a
// newer one). Conversations without a request aren't saved.
func Save(s *Session, seq int64) error {
	if !validID(s.ID) {
		return errors.New("invalid conversation id")
	}
	s.Prompts, s.Title = 0, ""
	var text strings.Builder
	for _, it := range s.Items {
		switch it.Kind {
		case "user":
			s.Prompts++
			if s.Title == "" {
				s.Title = oneLine(it.Text, 120)
			}
			if text.Len() < maxSearchText {
				text.WriteString(it.Text + "\n")
			}
		case "assistant":
			if text.Len() < maxSearchText {
				text.WriteString(it.Text + "\n")
			}
		}
	}
	if s.Prompts == 0 {
		return nil
	}
	s.Text = text.String()
	if len(s.Text) > maxSearchText {
		s.Text = strings.ToValidUTF8(s.Text[:maxSearchText], "")
	}
	mu.Lock()
	defer mu.Unlock()
	if seq < seqs[s.ID] {
		return nil
	}
	seqs[s.ID] = seq
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	full, err := json.Marshal(s)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(s.Meta)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, s.ID+".json"), full); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, s.ID+".meta.json"), meta); err != nil {
		return err
	}
	prune(dir)
	return nil
}

func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// prune removes the oldest conversations beyond Keep.
func prune(dir string) {
	list, err := list(dir)
	if err != nil || len(list) <= Keep {
		return
	}
	for _, m := range list[Keep:] {
		_ = os.Remove(filepath.Join(dir, m.ID+".json"))
		_ = os.Remove(filepath.Join(dir, m.ID+".meta.json"))
	}
}

// Load reads a saved conversation.
func Load(id string) (*Session, error) {
	if !validID(id) {
		return nil, errors.New("invalid conversation id")
	}
	b, err := os.ReadFile(filepath.Join(Dir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Delete removes a saved conversation.
func Delete(id string) error {
	if !validID(id) {
		return errors.New("invalid conversation id")
	}
	err := os.Remove(filepath.Join(Dir(), id+".json"))
	_ = os.Remove(filepath.Join(Dir(), id+".meta.json"))
	return err
}

// List returns the saved conversations, most recently used first.
func List() ([]Meta, error) { return list(Dir()) }

func list(dir string) ([]Meta, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.meta.json"))
	if err != nil {
		return nil, err
	}
	out := make([]Meta, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) == nil && validID(m.ID) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Latest returns the most recent conversation whose main folder is dir.
func Latest(dir string) (*Meta, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if len(all[i].Dirs) > 0 && samePath(all[i].Dirs[0], dir) {
			return &all[i], nil
		}
	}
	return nil, os.ErrNotExist
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// InFolder reports whether a conversation was in dir (its main folder or any other).
func (m Meta) InFolder(dir string) bool {
	for _, d := range m.Dirs {
		if samePath(d, dir) {
			return true
		}
	}
	return false
}

// Match reports whether every word of q appears in the conversation's title or text
// (case-insensitive), and returns a short excerpt around the first word when it is not in
// the title.
func (m Meta) Match(q string) (ok bool, excerpt string) {
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return true, ""
	}
	title := strings.ToLower(m.Title)
	text := strings.ToLower(m.Text)
	inTitle := true
	for _, w := range words {
		if !strings.Contains(title, w) {
			inTitle = false
			if !strings.Contains(text, w) {
				return false, ""
			}
		}
	}
	if inTitle {
		return true, ""
	}
	for _, w := range words {
		if strings.Contains(title, w) {
			continue
		}
		i := strings.Index(text, w)
		// lower-casing can change byte lengths; fall back to the lower-case text if so
		src := m.Text
		if len(src) != len(text) {
			src = text
		}
		start := max(i-40, 0)
		for start > 0 && !utf8Start(src[start]) {
			start--
		}
		end := min(i+len(w)+60, len(src))
		for end < len(src) && !utf8Start(src[end]) {
			end++
		}
		ex := oneLine(src[start:end], 200)
		if start > 0 {
			ex = "…" + ex
		}
		if end < len(src) {
			ex += "…"
		}
		return true, ex
	}
	return true, ""
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}
