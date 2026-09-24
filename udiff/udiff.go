// Package udiff renders and parses unified diff text.
package udiff

import (
	"fmt"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Diff builds a patch transforming a into b with C context lines.
func Diff(oldName, newName string, a, b []byte, C int, maxEdits int) (Patch, error) {
	d := edit.Differ{MaxEdits: maxEdits}
	segs, err := d.Diff(lines.Split(a), lines.Split(b))
	if err != nil {
		return Patch{}, err
	}
	return Patch{OldName: oldName, NewName: newName, Hunks: hunk.Build(segs, C)}, nil
}

// Render produces canonical unified diff text.
func (p Patch) Render() []byte {
	var buf []byte
	buf = append(buf, "--- "+p.OldName+"\n"...)
	buf = append(buf, "+++ "+p.NewName+"\n"...)
	for _, h := range p.Hunks {
		buf = append(buf, fmt.Sprintf("@@ -%s +%s @@\n", rangeSpec(h.OldStart, h.OldCount), rangeSpec(h.NewStart, h.NewCount))...)
		for _, r := range h.Rows {
			switch r.Kind {
			case hunk.Ctx:
				buf = append(buf, ' ')
				buf = append(buf, r.Line.Full()...)
				if len(r.Line.EOL) == 0 {
					buf = append(buf, '\n')
					buf = append(buf, noNL...)
					buf = append(buf, '\n')
				}
			case hunk.Old:
				buf = append(buf, '-')
				buf = append(buf, r.Line.Full()...)
				if r.OldNoNL {
					buf = append(buf, '\n')
					buf = append(buf, noNL...)
					buf = append(buf, '\n')
				}
			case hunk.New:
				buf = append(buf, '+')
				buf = append(buf, r.Line.Full()...)
				if r.NewNoNL {
					buf = append(buf, '\n')
					buf = append(buf, noNL...)
					buf = append(buf, '\n')
				}
			}
		}
	}
	return buf
}

func rangeSpec(start, count int) string {
	s := fmt.Sprintf("%d", start)
	if count != 1 {
		s += fmt.Sprintf(",%d", count)
	}
	return s
}
