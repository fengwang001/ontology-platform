package merge3

import "errors"

// ErrEmptyLabel is returned by Render when a label is the empty string.
var ErrEmptyLabel = errors.New("merge3: label must not be empty")

// baseLabel is the fixed label of the ancestor section in Render output.
const baseLabel = "base"

// Render formats the merge result in diff3 style:
//
//	<<<<<<< ourLabel
//	ours lines
//	||||||| base
//	base lines
//	=======
//	theirs lines
//	>>>>>>> theirLabel
//
// The base section always appears, even when empty. Without conflicts the
// output is exactly Result.Lines.
func (r Result) Render(ourLabel, theirLabel string) ([]string, error) {
	if ourLabel == "" || theirLabel == "" {
		return nil, ErrEmptyLabel
	}
	var out []string
	ci := 0
	for i := 0; i <= len(r.Lines); i++ {
		for ci < len(r.Conflicts) && r.Conflicts[ci].At <= i {
			c := r.Conflicts[ci]
			out = append(out, "<<<<<<< "+ourLabel)
			out = append(out, c.Ours...)
			out = append(out, "||||||| "+baseLabel)
			out = append(out, c.Base...)
			out = append(out, "=======")
			out = append(out, c.Theirs...)
			out = append(out, ">>>>>>> "+theirLabel)
			ci++
		}
		if i < len(r.Lines) {
			out = append(out, r.Lines[i])
		}
	}
	return out, nil
}
