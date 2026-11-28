package termimg

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestProbeRealTerminal asks the terminal it runs in, for checking a terminal by hand: build
// the test binary (go test -c) and run it in that terminal with WERKZ_PROBE_OUT=<file>.
func TestProbeRealTerminal(t *testing.T) {
	out := os.Getenv("WERKZ_PROBE_OUT")
	if out == "" {
		t.Skip("set WERKZ_PROBE_OUT and run the test binary in a terminal")
	}
	c := Probe(context.Background())
	if err := os.WriteFile(out, fmt.Appendf(nil, "%+v\n", c), 0o600); err != nil {
		t.Fatal(err)
	}
}
