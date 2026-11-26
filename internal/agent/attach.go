package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/mention"
	"github.com/TonmoyTalukder/werkz/internal/tools"
)

// @mentions: files named in a message are attached to it, so the model has them without a
// Read round trip. The same rules as the Read tool apply: credential files are never opened,
// and files outside the working folders aren't attached.

const (
	attachMaxLines = 2000
	attachMaxBytes = 100 << 10
	attachMaxFiles = 20
	attachDirMax   = 200
)

// Attachment is how one mention was handled, for the transcript.
type Attachment struct {
	Mention string // as written, e.g. "@src/a.go:10-20"
	Label   string // e.g. "src/a.go (120 lines)"
	OK      bool   // attached (in full or in part)
}

// AttachedMsg lists the files attached to the message just sent.
type AttachedMsg struct{ Items []Attachment }

// attachMentions returns the text to send for a message: the message itself followed by the
// files it mentions. It also reports each mention for the transcript.
func (e *Engine) attachMentions(text string) (string, []Attachment) {
	toks := mention.Find(text)
	if len(toks) == 0 {
		return text, nil
	}
	dirs := e.dirs()
	var b strings.Builder
	var items []Attachment
	seen := map[string]bool{}
	for _, t := range toks {
		raw := t.Raw(text)
		abs, ok := mention.Resolve(t.Path, dirs)
		if !ok {
			continue // not a file (an @handle, a decorator...): leave it as text, say nothing
		}
		key := fmt.Sprintf("%s:%d-%d", abs, t.From, t.To)
		if seen[key] {
			continue
		}
		seen[key] = true
		show := mention.Display(abs, dirs)
		if len(items) >= attachMaxFiles {
			items = append(items, Attachment{Mention: raw, Label: show + " (not attached: " + fmt.Sprint(attachMaxFiles) + " files at most)"})
			continue
		}
		if tools.Sensitive(abs) {
			items = append(items, Attachment{Mention: raw, Label: show + " (credential file, never attached)"})
			continue
		}
		if !tools.InsideDirs(abs, dirs) {
			items = append(items, Attachment{Mention: raw, Label: show + " (outside your folders, not attached)"})
			continue
		}
		body, label, ok := e.attachOne(abs, show, t)
		items = append(items, Attachment{Mention: raw, Label: label, OK: ok})
		if body != "" {
			b.WriteString("\n\n" + body)
		}
	}
	if b.Len() == 0 {
		return text, items
	}
	return text + "\n\n<attachments note=\"Files the user mentioned with @, as they are now. Line numbers are for reference only.\">" +
		b.String() + "\n</attachments>", items
}

func (e *Engine) attachOne(abs, show string, t mention.Token) (body, label string, ok bool) {
	st, err := os.Stat(abs)
	if err != nil {
		return "", show + " (" + err.Error() + ")", false
	}
	if st.IsDir() {
		list, n := listDir(abs)
		more := ""
		if n > attachDirMax {
			more = fmt.Sprintf("\n… %d more entries", n-attachDirMax)
		}
		return fmt.Sprintf("<folder path=%q>\n%s%s\n</folder>", show, list, more),
			fmt.Sprintf("%s/ (%d entries)", strings.TrimSuffix(show, "/"), n), true
	}
	if e.attached == nil {
		e.attached = map[string]time.Time{}
	}
	key := fmt.Sprintf("%s:%d-%d", abs, t.From, t.To)
	if at, ok := e.attached[key]; ok && at.Equal(st.ModTime()) {
		return fmt.Sprintf("<file path=%q>(unchanged since it was attached earlier in this conversation)</file>", show),
			show + " (unchanged, attached earlier)", true
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", show + " (" + err.Error() + ")", false
	}
	defer f.Close()
	head := make([]byte, 8000)
	n, _ := f.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return fmt.Sprintf("<file path=%q>(binary file, %d KB, not shown)</file>", show, st.Size()>>10),
			show + " (binary, path only)", true
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", show + " (" + err.Error() + ")", false
	}
	from, to := 1, 0
	if t.From > 0 {
		from, to = t.From, t.To
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	var sb strings.Builder
	line, shown, last, cut := 0, 0, 0, false
	for sc.Scan() {
		line++
		if line < from || (to > 0 && line > to) {
			continue
		}
		if shown >= attachMaxLines || sb.Len() >= attachMaxBytes {
			cut = true
			continue // keep counting lines
		}
		l := sc.Text()
		if len(l) > 2000 {
			l = strings.ToValidUTF8(l[:2000], "") + "… (line cut)"
		}
		fmt.Fprintf(&sb, "%6d\t%s\n", line, l)
		shown++
		last = line
	}
	total := line
	rng := ""
	switch {
	case t.From > 0:
		rng = fmt.Sprintf(" lines=\"%d-%d\"", from, last)
		label = fmt.Sprintf("%s (lines %d–%d)", show, from, last)
	case cut:
		rng = fmt.Sprintf(" lines=\"1-%d\" total=\"%d\"", last, total)
		label = fmt.Sprintf("%s (first %d of %d lines)", show, shown, total)
	default:
		label = fmt.Sprintf("%s (%d lines)", show, total)
	}
	if t.From > 0 && shown == 0 {
		return "", fmt.Sprintf("%s (has %d lines, none in %d–%d)", show, total, t.From, t.To), false
	}
	out := sb.String()
	if cut {
		out += fmt.Sprintf("… (cut at line %d to save context; use Read with offset=%d for more)\n", last, last+1)
	}
	e.reads.Mark(abs) // the model has seen it, so it may edit it (as after a Read)
	e.attached[key] = st.ModTime()
	return fmt.Sprintf("<file path=%q%s>\n%s</file>", show, rng, out), label, true
}

// listDir lists a folder two levels deep (folders end in "/"), skipping heavy folders.
func listDir(root string) (string, int) {
	var out []string
	entries, _ := os.ReadDir(root)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, d := range entries {
		if d.Name() == ".git" {
			continue
		}
		if !d.IsDir() {
			out = append(out, d.Name())
			continue
		}
		out = append(out, d.Name()+"/")
		if skipListing[d.Name()] {
			continue
		}
		sub, _ := os.ReadDir(filepath.Join(root, d.Name()))
		for _, s := range sub {
			n := "  " + d.Name() + "/" + s.Name()
			if s.IsDir() {
				n += "/"
			}
			out = append(out, n)
		}
	}
	n := len(out)
	if n > attachDirMax {
		out = out[:attachDirMax]
	}
	return strings.Join(out, "\n"), n
}

var skipListing = map[string]bool{"node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	"target": true, "dist": true, "build": true, ".next": true, ".cache": true, "vendor": true}
