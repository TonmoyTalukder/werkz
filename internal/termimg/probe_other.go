//go:build !unix

package termimg

import (
	"context"
	"os"
)

// Probe on systems without the terminal calls the query needs (Windows,
// Plan 9, js): it answers from the environment only and queries nothing.
func Probe(context.Context) Caps { return FromEnv(os.Getenv) }
