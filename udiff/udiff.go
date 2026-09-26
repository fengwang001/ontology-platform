// Package udiff renders and parses unified diff text.
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// Patch is a parsed or constructed unified diff.
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Limits bounds parsing effort. Zero means unbounded.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// FormatError points at a malformed line in the patch text.
type FormatError struct {
	Line    int
	Message string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: format error at line %d: %s", e.Line, e.Message)
}

// IsFormat reports whether err is a patch-format error.
func IsFormat(err error) bool {
	var fe *FormatError
	return errors.As(err, &fe)
}

// Make builds a shortest-script patch for a -> b with c context lines.
func Make(a, b []byte, c, maxDist int) (Patch, error) {
	al, bl := lines.Split(a), lines.Split(b)
	eng := edit.Engine{MaxDistance: maxDist}
	es, err := eng.Diff(al, bl)
	if err != nil {
		return Patch{}, err
	}
	return Patch{OldName: "a", NewName: "b", Hunks: hunk.Group(es, c)}, nil
}

// Render writes the canonical unified-diff text.
func Render(p Patch) []byte {
	var w bytes.Buffer
	fmt.Fprintf(&w, "--- %s\n", p.OldName)
	fmt.Fprintf(&w, "+++ %s\n", p.NewName)
	for _, h := range p.Hunks {
		fmt.Fprintf(&w, "@@ -%s +%s @@\n", head(h.OldStart, h.OldCount),
			head(h.NewStart, h.NewCount))
		oldSide, newSide := true, true
		for i, e := range h.Edits {
			switch e.Op {
			case edit.Equal:
				put(&w, ' ', e.A)
			case edit.Delete:
				put(&w, '-', e.A)
			case edit.Insert:
				put(&w, '+', e.B)
			}
			if i == len(h.Edits)-1 {
				oldSide, newSide = false, false
			}
			_ = oldSide
			_ = newSide
		}
		writeMarkers(&w, h)
	}
	return w.Bytes()
}

func head(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func put(w *bytes.Buffer, p byte, l lines.Line) {
	w.WriteByte(p)
	w.Write(l.Data)
	w.Write(l.EOL)
}

func writeMarkers(w *bytes.Buffer, h hunk.Hunk) {
	// Markers belong to the last old/new line; emitted after the final edit
	// when that side ends without a newline.
	lastOld, lastNew := lastLine(h.Edits, false), lastLine(h.Edits, true)
	if lastOld != nil && len(lastOld.EOL) == 0 {
		w.WriteString("\\ No newline at end of file\n")
	}
	if lastNew != nil && len(lastNew.EOL) == 0 && !sameLast(h.Edits) {
		w.WriteString("\\ No newline at end of file\n")
	}
}

func lastLine(es []edit.Edit, sideNew bool) *lines.Line {
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		if sideNew && e.Op != edit.Delete {
			return &e.B
		}
		if !sideNew && e.Op != edit.Insert {
			return &e.A
		}
	}
	return nil
}

func sameLast(es []edit.Edit) bool {
	if len(es) == 0 {
		return false
	}
	e := es[len(es)-1]
	return e.Op == edit.Equal
}
