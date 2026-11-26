package termimg

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"time"
)

const probeTimeout = 400 * time.Millisecond

// probeQuery asks for Kitty graphics support, the cell size in pixels
// (CSI 16 t) and, last, the primary device attributes (DA1). Terminals answer
// in order, so the DA1 reply marks the end of the conversation.
const probeQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\" + "\x1b[16t" + "\x1b[c"

type probeResult struct {
	kitty        bool
	sixel        bool
	gotDA1       bool
	cellW, cellH int
}

// mergeProbe folds a probe result into caps; explicit/env answers win.
func mergeProbe(caps *Caps, res probeResult) {
	if caps.CellW == 0 || caps.CellH == 0 {
		caps.CellW, caps.CellH = res.cellW, res.cellH
	}
	if caps.Proto != None {
		return
	}
	switch {
	case res.kitty:
		caps.Proto = Kitty // placeholders only when env identified kitty/ghostty
	case res.sixel:
		caps.Proto = Sixel
	}
}

// probeWith writes the query to out and parses replies from in until the DA1
// answer or the timeout.
func probeWith(in io.Reader, out io.Writer, timeout time.Duration) probeResult {
	var res probeResult
	if _, err := io.WriteString(out, probeQuery); err != nil {
		return res
	}
	data := make(chan []byte)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				select {
				case data <- append([]byte(nil), buf[:n]...):
				case <-stop:
					return
				}
			}
			if err != nil {
				select {
				case data <- nil:
				case <-stop:
				}
				return
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var acc []byte
	for {
		select {
		case b := <-data:
			if b == nil {
				return res
			}
			acc = append(acc, b...)
			if parseReplies(acc, &res) {
				return res
			}
		case <-timer.C:
			return res
		}
	}
}

// parseReplies scans accumulated reply bytes; it reports true once DA1 seen.
func parseReplies(acc []byte, res *probeResult) bool {
	*res = probeResult{}
	for i := 0; i < len(acc); i++ {
		if acc[i] != 0x1b || i+1 >= len(acc) {
			continue
		}
		switch acc[i+1] {
		case '_': // APC: kitty graphics reply, ends with ST
			end := bytes.Index(acc[i:], []byte(st))
			if end < 0 {
				return false
			}
			body := string(acc[i+2 : i+end])
			if strings.HasPrefix(body, "G") && strings.Contains(body, "i=31") &&
				strings.Contains(body, ";OK") {
				res.kitty = true
			}
			i += end + 1
		case '[': // CSI
			j := i + 2
			for j < len(acc) && (acc[j] < 0x40 || acc[j] > 0x7e) {
				j++
			}
			if j >= len(acc) {
				return false
			}
			params, final := string(acc[i+2:j]), acc[j]
			switch {
			case final == 'c' && strings.HasPrefix(params, "?"):
				for _, a := range strings.Split(params[1:], ";") {
					if a == "4" {
						res.sixel = true
					}
				}
				res.gotDA1 = true
			case final == 't' && strings.HasPrefix(params, "6;"):
				f := strings.Split(params, ";")
				if len(f) == 3 {
					h, _ := strconv.Atoi(f[1])
					w, _ := strconv.Atoi(f[2])
					res.cellH, res.cellW = h, w
				}
			}
			i = j
		}
	}
	return res.gotDA1
}
