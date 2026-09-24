package udiff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ontology/hunk"
	"ontology/lines"
)

const noNL = `\ No newline at end of file`

// FormatError is a malformed patch. Line is 1-based within the patch text.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("malformed patch at line %d: %s", e.Line, e.Msg)
}

// Limits bound parser resource use.
type Limits struct {
	MaxBytes, MaxHunks int
}

// ErrTooLarge reports a resource limit was exceeded.
var ErrTooLarge = errors.New("udiff: patch exceeds size or hunk limit")

// Parse strictly parses unified diff text. Declared hunk counts must match the
// body exactly; every body prefix must be one of space, '-', '+', or '\'.
func Parse(data []byte, lim Limits) (Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return Patch{}, ErrTooLarge
	}
	rl := lines.Split(data)
	if len(rl) < 2 || !strings.HasPrefix(string(rl[0].Full()), "--- ") ||
		!strings.HasPrefix(string(rl[1].Full()), "+++ ") {
		return Patch{}, &FormatError{Line: 1, Msg: "missing file headers"}
	}
	p := Patch{OldName: name(rl[0]), NewName: name(rl[1])}
	i := 2
	for i < len(rl) {
		hdr := i + 1
		os, oc, ns, nc, err := parseHeader(rl[i].Full())
		if err != nil {
			return Patch{}, &FormatError{Line: hdr, Msg: err.Error()}
		}
		h := hunk.Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}
		i++
		ocSeen, ncSeen := 0, 0
		for (ocSeen < oc || ncSeen < nc) && i < len(rl) {
			full := rl[i].Full()
			k := full[0]
			if k != ' ' && k != '-' && k != '+' {
				return Patch{}, &FormatError{Line: i + 1, Msg: "unexpected line prefix"}
			}
			sub := lines.Split(full[1:])
			if len(sub) != 1 {
				return Patch{}, &FormatError{Line: i + 1, Msg: "bad body line"}
			}
			ln := sub[0]
			var kind hunk.Kind
			if k == ' ' {
				kind = hunk.Ctx
			} else if k == '-' {
				kind = hunk.Old
			} else {
				kind = hunk.New
			}
			row := hunk.Row{Kind: kind, Line: ln}
			if k == ' ' || k == '-' {
				ocSeen++
			}
			if k == ' ' || k == '+' {
				ncSeen++
			}
			if ln.EOL == nil {
				if i+1 >= len(rl) || string(rl[i+1].Full()) != noNL+"\n" {
					return Patch{}, &FormatError{Line: i + 1, Msg: "missing no-newline marker"}
				}
				switch k {
				case '-':
					row.OldNoNL = true
				case '+':
					row.NewNoNL = true
				default:
					row.OldNoNL, row.NewNoNL = true, true
				}
				h.Rows = append(h.Rows, row, hunk.Row{Kind: hunk.Mark, OldNoNL: row.OldNoNL, NewNoNL: row.NewNoNL})
				i++
			} else {
				h.Rows = append(h.Rows, row)
			}
			i++
		}
		if ocSeen != oc || ncSeen != nc {
			return Patch{}, &FormatError{Line: hdr, Msg: "hunk body count mismatch"}
		}
		p.Hunks = append(p.Hunks, h)
		if lim.MaxHunks > 0 && len(p.Hunks) > lim.MaxHunks {
			return Patch{}, ErrTooLarge
		}
	}
	return p, nil
}

func name(l lines.Line) string {
	return strings.TrimSuffix(string(l.Content)[4:], "\r")
}

func parseHeader(full []byte) (os, oc, ns, nc int, err error) {
	s := string(full)
	s = strings.TrimRight(s, "\r\n")
	if !strings.HasPrefix(s, "@@ ") || !strings.HasSuffix(s, " @@") {
		err = errors.New("bad hunk header")
		return
	}
	parts := strings.SplitN(s[3:len(s)-3], " ", 2)
	if len(parts) != 2 || parts[0][0] != '-' || parts[1][0] != '+' {
		err = errors.New("bad hunk ranges")
		return
	}
	if os, oc, err = parseRange(parts[0][1:]); err != nil {
		return
	}
	ns, nc, err = parseRange(parts[1][1:])
	return
}

func parseRange(s string) (start, count int, err error) {
	if i := strings.IndexByte(s, ','); i >= 0 {
		if start, err = strconv.Atoi(s[:i]); err == nil {
			count, err = strconv.Atoi(s[i+1:])
		}
	} else if start, err = strconv.Atoi(s); err == nil {
		count = 1
	}
	if err != nil || start < 0 || count < 0 {
		err = errors.New("bad range number")
	}
	return
}
