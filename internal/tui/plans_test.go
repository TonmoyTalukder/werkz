package tui

import (
	"strings"
	"testing"
)

// Over SSH (or without a desktop) the sign-in can't open a browser: the help says how to finish
// it from another computer, with the exact port to forward.
func TestSignInHelpWithoutBrowser(t *testing.T) {
	u := "https://auth.openai.com/api/accounts/authorize?client_id=x&redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fauth%2Fcallback"
	if got := callbackPort(u); got != "1455" {
		t.Fatalf("port %q", got)
	}
	h := signInHelp(u, false)
	if !strings.Contains(h, "ssh -L 1455:127.0.0.1:1455") || !strings.Contains(h, u) {
		t.Fatalf("help: %s", h)
	}
	t.Setenv("SSH_CONNECTION", "10.0.0.2 5555 10.0.0.1 22")
	if hasBrowser() {
		t.Fatal("an SSH session has no local browser")
	}
	if err := openBrowser(u); err == nil {
		t.Fatal("openBrowser must not try over SSH")
	}
}
