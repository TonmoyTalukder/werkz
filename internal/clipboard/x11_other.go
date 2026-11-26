//go:build !unix || darwin

package clipboard

import (
	"context"
	"errors"
	"io"
)

// ServeArg is the hidden command-line argument that runs the X11 clipboard helper (unused here).
const ServeArg = "__werkz-clipboard-x11"

func x11Copy(context.Context, string) error { return errors.New("no X11 clipboard on this system") }

// Serve is only available where X11 is.
func Serve(io.Reader, io.Writer) error { return errors.New("no X11 clipboard on this system") }
