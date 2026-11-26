package llm

import (
	"fmt"

	"github.com/TonmoyTalukder/werkz/internal/textutil"
)

type HTTPError struct {
	Status int
	Body   string
	// Reason, when set, is what to tell the user instead of the raw body; Fatal means the
	// provider can't be used again this session (e.g. the sign-in was rejected).
	Reason string
	Fatal  bool
}

func (e *HTTPError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, textutil.Truncate(e.Body, 300))
}
