package merge3

import "errors"

// ErrEmptyLabel is returned by Render when either label is empty.
var ErrEmptyLabel = errors.New("merge3: conflict label must not be empty")

// Render returns the merged lines with conflicts expressed in diff3
// style:
//
//	<<<<<<< ourLabel
//	ours lines
//	||||||| base
//	base lines
//	=======
//	theirs lines
//	>>>>>>> theirLabel
//
// The base section always appears, even when it has no content
// lines. It returns ErrEmptyLabel if either label is empty.
func (r Result) Render(ourLabel, theirLabel string) ([]string, error) {
	if ourLabel == "" || theirLabel == "" {
		return nil, ErrEmptyLabel
	}
	var out []string
	ci := 0
	emitConflicts := func(line int) {
		for ci < len(r.Conflicts) && r.Conflicts[ci].Line == line {
			c := r.Conflicts[ci]
			out = append(out, "<<<<<<< "+ourLabel)
			out = append(out, c.Ours...)
			out = append(out, "||||||| base")
			out = append(out, c.Base...)
			out = append(out, "=======")
			out = append(out, c.Theirs...)
			out = append(out, ">>>>>>> "+theirLabel)
			ci++
		}
	}
	for idx, line := range r.Lines {
		emitConflicts(idx)
		out = append(out, line)
	}
	emitConflicts(len(r.Lines))
	return out, nil
}
