package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
)

// ReadTracker remembers each file's mtime when the model read it, so edits of unread or
// since-changed files are refused (the model would otherwise edit blind or rewrite whole files).
type ReadTracker struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func NewReadTracker() *ReadTracker { return &ReadTracker{m: map[string]time.Time{}} }

func (r *ReadTracker) mark(path string) {
	if r == nil {
		return
	}
	if st, err := os.Stat(path); err == nil {
		r.mu.Lock()
		r.m[absPath(path)] = st.ModTime()
		r.mu.Unlock()
	}
}

func (r *ReadTracker) check(path string) error {
	if r == nil {
		return nil
	}
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // a new file: nothing to have read
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	t, ok := r.m[absPath(path)]
	r.mu.Unlock()
	switch {
	case !ok:
		return fmt.Errorf("read %s with the Read tool before changing it (at least the part you will edit)", RelPath(path))
	case !t.Equal(st.ModTime()):
		return fmt.Errorf("%s changed on disk since you last read it; read it again before changing it", RelPath(path))
	}
	return nil
}

// Mark records that the model has seen path as it is now (e.g. attached with an @mention).
func (r *ReadTracker) Mark(path string) { r.mark(path) }

// Check reports why path may not be changed yet (nil = it may).
func (r *ReadTracker) Check(path string) error { return r.check(path) }
