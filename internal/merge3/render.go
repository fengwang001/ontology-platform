package merge3

import (
	"errors"
	"fmt"
)

// ErrEmptyLabel is returned by Render when either label is empty.
var ErrEmptyLabel = errors.New("merge3: render labels must not be empty")

// Render formats the merge result in diff3 style. Clean regions are emitted
// as-is. Each conflict becomes a <<<<<<< ourLabel section with ours lines,
// a ||||||| base section with base lines (always present, even when empty),
// a ======= separator, theirs lines, and a >>>>>>> theirLabel trailer.
func (r Result) Render(ourLabel, theirLabel string) ([]string, error) {
	if ourLabel == "" || theirLabel == "" {
		return nil, fmt.Errorf("%w (ours=%q theirs=%q)",
			ErrEmptyLabel, ourLabel, theirLabel)
	}
	var out []string
	for _, seg := range r.segments {
		if seg.conflict < 0 {
			out = append(out, seg.lines...)
			continue
		}
		c := r.Conflicts[seg.conflict]
		out = append(out, "<<<<<<< "+ourLabel)
		out = append(out, c.Ours...)
		out = append(out, "||||||| base")
		out = append(out, c.Base...)
		out = append(out, "=======")
		out = append(out, c.Theirs...)
		out = append(out, ">>>>>>> "+theirLabel)
	}
	return out, nil
}
