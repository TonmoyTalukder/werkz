package llm

import (
	"fmt"

	"github.com/TonmoyTalukder/werkz/internal/textutil"
)

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, textutil.Truncate(e.Body, 300))
}
