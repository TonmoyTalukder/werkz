package browser

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// profileLocked reports whether a live Chrome on this host holds dir: Chrome
// keeps a SingletonLock symlink there whose target is "<host>-<pid>".
func profileLocked(dir string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndexByte(target, '-')
	if i < 0 {
		return false
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return false
	}
	if host, err := os.Hostname(); err != nil || host != target[:i] {
		return false // another machine's lock (shared home): Chrome itself decides
	}
	err = syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// pickProfile returns the profile directory to use and whether it is a
// per-process fallback that Close must remove. When another live Chrome (for
// example a second werkz) holds dir, it falls back to dir-<pid>.
func pickProfile(dir string) (string, bool) {
	if !profileLocked(dir) {
		return dir, false
	}
	base := dir + "-" + strconv.Itoa(os.Getpid())
	cand := base
	for n := 2; profileLocked(cand); n++ {
		cand = base + "-" + strconv.Itoa(n)
	}
	return cand, true
}
