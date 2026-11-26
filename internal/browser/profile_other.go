//go:build !unix

package browser

// pidAlive treats a lock as live where a pid cannot be probed without
// side effects: the profile then falls back to a per-process directory.
func pidAlive(int) bool { return true }
