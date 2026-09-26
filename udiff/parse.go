package udiff

import (
	"bytes"
	"fmt"

	"ontology/lines"
)

// Parse strictly parses unified diff text under the given limits.
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && int64(len(data)) > lim.MaxBytes {
		return nil, fmt.Errorf("%w: %d bytes exceed limit %d", ErrFormat, len(data), lim.MaxBytes)
	}
	raw := bytes.Split(data, []byte{'\n'})
	if len(raw) > 0 && len(raw[len(raw)-1]) == 0 {
		raw = raw[:len(raw)-1] // trailing newline after final line
	}
	if len(raw) < 2 || !bytes.HasPrefix(raw[0], []byte("--- ")) ||
		!bytes.HasPrefix(raw[1], []byte("+++ ")) {
		return nil, &ParseError{Line: 1, Msg: "missing ---/+++ headers"}
	}
	p := &Patch{OldName: string(raw[0][4:]), NewName: string(raw[1][4:])}
	ln := 2
	for ln < len(raw) {
		h, n, err := parseHunk(raw, ln, lim, len(p.Hunks))
		if err != nil {
			return nil, err
		}
		p.Hunks = append(p.Hunks, h)
		ln = n
	}
	if len(p.Hunks) == 0 {
		return nil, &ParseError{Line: 1, Msg: "patch contains no hunks"}
	}
	return p, nil
}

func parseHunk(raw [][]byte, start int, lim Limits, hunkIndex int) (Hunk, int, error) {
	var h Hunk
	head := string(raw[start])
	var rl string
	if n, _ := fmt.Sscanf(head, "@@ -%d,%d +%d,%d @@",
		&h.OldStart, &h.OldCount, &h.NewStart, &h.NewCount); n == 4 {
	} else if n, _ = fmt.Sscanf(head, "@@ -%d,%d +%d @@",
		&h.OldStart, &h.OldCount, &h.NewStart); n == 3 {
		h.NewCount = 1
	} else if n, _ = fmt.Sscanf(head, "@@ -%d +%d,%d @@",
		&h.OldStart, &h.NewStart, &h.NewCount); n == 3 {
		h.OldCount = 1
	} else if n, _ = fmt.Sscanf(head, "@@ -%d +%d @@", &h.OldStart, &h.NewStart); n == 2 {
		h.OldCount, h.NewCount = 1, 1
	} else {
		_ = rl
		return Hunk{}, start + 1, &ParseError{Line: start + 1, Msg: "bad hunk header"}
	}
	if h.OldCount < 0 || h.NewCount < 0 ||
		h.OldCount > len(raw) || h.NewCount > len(raw) {
		return Hunk{}, start + 1, &ParseError{Line: start + 1, Msg: "bad counts in header"}
	}
	if lim.MaxHunks > 0 && hunkIndex+1 > lim.MaxHunks {
		return Hunk{}, start + 1, &ParseError{Line: start + 1, Msg: "hunk count exceeds limit"}
	}
	ln := start + 1
	oc, nc := 0, 0
	for oc < h.OldCount || nc < h.NewCount {
		if ln >= len(raw) {
			return Hunk{}, ln + 1, &ParseError{Line: ln + 1, Msg: "truncated hunk body"}
		}
		r := raw[ln]
		if len(r) == 0 || (r[0] != ' ' && r[0] != '-' && r[0] != '+') {
			return Hunk{}, ln + 1, &ParseError{Line: ln + 1, Msg: "bad body prefix"}
		}
		row := Row{Mark: r[0], OldNL: true, NewNL: true}
		payload := r[1:]
		nl := true
		if ln+1 < len(raw) && string(raw[ln+1]) == NoNLText {
			nl = false
			ln++
		}
		if nl {
			payload = append(append([]byte{}, payload...), '\n')
		}
		l := lines.Line{Data: payload}
		switch row.Mark {
		case ' ':
			row.Old, row.New = &l, &l
			row.OldNL, row.NewNL = nl, nl
			oc++
			nc++
		case '-':
			row.Old = &l
			row.OldNL = nl
			oc++
		case '+':
			row.New = &l
			row.NewNL = nl
			nc++
		}
		h.Rows = append(h.Rows, row)
		ln++
	}
	if ln < len(raw) && string(raw[ln]) == NoNLText {
		return Hunk{}, ln + 1, &ParseError{Line: ln + 1, Msg: "stray no-newline marker"}
	}
	return h, ln, nil
}
