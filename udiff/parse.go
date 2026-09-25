package udiff

import (
	"fmt"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/lines"
)

// Parse decodes unified diff text, enforcing exact declared counts.
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, ErrTooLarge
	}
	rows := splitText(data)
	if len(rows) < 2 {
		return nil, &MalformedError{TextLine: len(rows) + 1, Reason: "missing file headers"}
	}
	if !strings.HasPrefix(rows[0], "--- ") || !strings.HasPrefix(rows[1], "+++ ") {
		return nil, &MalformedError{TextLine: 1, Reason: "bad file header"}
	}
	p := &Patch{OldName: rows[0][4:], NewName: rows[1][4:]}
	i := 2
	for i < len(rows) {
		lineNo := i + 1
		os, on, ns, nn, err := parseHeader(rows[i])
		if err != nil {
			return nil, &MalformedError{TextLine: lineNo, Reason: err.Error()}
		}
		i++
		if lim.MaxHunks > 0 && len(p.Hunks)+1 > lim.MaxHunks {
			return nil, ErrTooLarge
		}
		h := Hunk{OldStart: os, OldN: on, NewStart: ns, NewN: nn}
		oc, nc := 0, 0
		for oc < on || nc < nn {
			if i >= len(rows) {
				return nil, &MalformedError{TextLine: len(rows) + 1, Reason: "truncated hunk body"}
			}
			text := rows[i]
			i++
			if text == NoNL {
				if len(h.Lines) == 0 {
					return nil, &MalformedError{TextLine: i, Reason: "stray no-newline marker"}
				}
				h.Lines[len(h.Lines)-1].NoNL = true
				continue
			}
			if len(text) == 0 {
				return nil, &MalformedError{TextLine: i, Reason: "line missing leading prefix"}
			}
			switch text[0] {
			case ' ':
				oc++
				nc++
			case '-':
				oc++
			case '+':
				nc++
			default:
				return nil, &MalformedError{TextLine: i, Reason: "line prefix must be space, -, or +"}
			}
			h.Lines = append(h.Lines, Line{Kind: prefixKind(text[0]), Line: parseLine(text[1:])})
		}
		if i < len(rows) && rows[i] == NoNL {
			h.Lines[len(h.Lines)-1].NoNL = true
			i++
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p, nil
}

func prefixKind(c byte) edit.Kind {
	switch c {
	case '-':
		return edit.Delete
	case '+':
		return edit.Insert
	}
	return edit.Equal
}

func parseLine(s string) lines.Line {
	if strings.HasSuffix(s, "\r\n") {
		return lines.Line{Content: []byte(s[:len(s)-2]), End: "\r\n"}
	}
	if strings.HasSuffix(s, "\n") {
		return lines.Line{Content: []byte(s[:len(s)-1]), End: "\n"}
	}
	return lines.Line{Content: []byte(s)}
}

func splitText(data []byte) []string {
	var rows []string
	for len(data) > 0 {
		j := strings.IndexByte(string(data), '\n')
		if j < 0 {
			rows = append(rows, string(data))
			break
		}
		rows = append(rows, string(data[:j+1]))
		data = data[j+1:]
	}
	return rows
}

func parseHeader(s string) (int, int, int, int, error) {
	if !strings.HasPrefix(s, "@@ ") || !strings.HasSuffix(s, " @@\n") {
		return 0, 0, 0, 0, fmt.Errorf("bad hunk header")
	}
	mid := strings.TrimSuffix(s[3:], " @@\n")
	parts := strings.SplitN(mid, " ", 2)
	if len(parts) != 2 || parts[0][0] != '-' || parts[1][0] != '+' {
		return 0, 0, 0, 0, fmt.Errorf("bad hunk header")
	}
	os, on, err := parseRange(parts[0][1:])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	ns, nn, err := parseRange(parts[1][1:])
	return os, on, ns, nn, err
}

func parseRange(s string) (int, int, error) {
	start, count := s, 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		start, count = s[:i], 0
		n, err := strconv.Atoi(s[i+1:])
		if err != nil || n < 0 {
			return 0, 0, fmt.Errorf("bad range count")
		}
		count = n
	}
	st, err := strconv.Atoi(start)
	if err != nil || st < 0 {
		return 0, 0, fmt.Errorf("bad range start")
	}
	return st, count, nil
}
