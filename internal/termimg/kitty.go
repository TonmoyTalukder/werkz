package termimg

import (
	"encoding/base64"
	"strconv"
	"strings"
)

const kittyChunk = 4096

// KittyTransmit returns the escape sequence(s) that upload a PNG as image id
// (base64 in chunks of at most 4096 bytes) and create a virtual placement
// (U=1) of cols×rows cells for it, to be displayed via KittyPlaceholder.
// Protocol: https://sw.kovidgoyal.net/kitty/graphics-protocol/
func KittyTransmit(id, placement uint32, png []byte, cols, rows int) string {
	data := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	first := true
	for len(data) > 0 || first {
		n := min(kittyChunk, len(data))
		chunk := data[:n]
		data = data[n:]
		more := "0"
		if len(data) > 0 {
			more = "1"
		}
		b.WriteString("\x1b_G")
		if first {
			b.WriteString("a=t,f=100,q=2,i=" + strconv.FormatUint(uint64(id), 10) + ",")
			first = false
		}
		b.WriteString("m=" + more + ";" + chunk + st)
	}
	b.WriteString("\x1b_Ga=p,U=1,i=" + strconv.FormatUint(uint64(id), 10))
	if placement != 0 {
		b.WriteString(",p=" + strconv.FormatUint(uint64(placement), 10))
	}
	b.WriteString(",c=" + strconv.Itoa(cols) + ",r=" + strconv.Itoa(rows) + ",q=2" + st)
	return b.String()
}

// KittyDelete frees image id and its placements.
func KittyDelete(id uint32) string {
	return "\x1b_Ga=d,d=I,i=" + strconv.FormatUint(uint64(id), 10) + ",q=2" + st
}

// placeholderRune is the Kitty Unicode placeholder character.
const placeholderRune = '\U0010EEEE'

// KittyPlaceholder returns rows lines of cols cells each. Every cell is
// U+10EEEE followed by the row and column diacritics; the image id is encoded
// in the foreground colour (low 24 bits; bits 24-31 as a third diacritic on
// the first cell of each line) and a non-zero placement id in the underline
// colour. Each line is self-contained (colours are reset at its end) and uses
// no cursor movement. Rows and columns beyond the diacritic table are clamped.
// Spec: https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders
func KittyPlaceholder(id, placement uint32, cols, rows int) []string {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	rgb := func(code string, v uint32) string {
		return "\x1b[" + code + ";2;" + strconv.Itoa(int(v>>16&255)) + ";" +
			strconv.Itoa(int(v>>8&255)) + ";" + strconv.Itoa(int(v&255)) + "m"
	}
	prefix := rgb("38", id)
	suffix := "\x1b[39m"
	if placement != 0 {
		prefix += rgb("58", placement)
		suffix += "\x1b[59m"
	}
	dia := func(i int) rune { return diacritics[min(i, len(diacritics)-1)] }
	lines := make([]string, rows)
	for r := range rows {
		var b strings.Builder
		b.WriteString(prefix)
		for c := range cols {
			b.WriteRune(placeholderRune)
			b.WriteRune(dia(r))
			b.WriteRune(dia(c))
			if c == 0 && id>>24 != 0 {
				b.WriteRune(dia(int(id >> 24)))
			}
		}
		b.WriteString(suffix)
		lines[r] = b.String()
	}
	return lines
}
