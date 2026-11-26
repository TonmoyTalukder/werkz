//go:build !unix

package chatgpt

// lockFile is a no-op where file locks aren't available; refreshes are still serialized within
// one werkz process.
func lockFile(string) (func(), error) { return func() {}, nil }
