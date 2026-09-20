package merge3

import "errors"

// ErrEmptyLabel is returned by Render when either merge label is empty.
var ErrEmptyLabel = errors.New("merge3: merge labels must not be empty")

// Render formats the merge result in diff3 style. Conflicts are emitted as:
//
//	<<<<<<< ourLabel
//	ours lines
//	||||||| base
//	base lines
//	=======
//	theirs lines
//	>>>>>>> theirLabel
//
// The base section always appears, even when empty.
func (r Result) Render(ourLabel, theirLabel string) ([]string, error) {
	if ourLabel == "" || theirLabel == "" {
		return nil, ErrEmptyLabel
	}
	var out []string
	pos := 0
	for _, c := range r.Conflicts {
		out = append(out, r.Lines[pos:c.LineIndex]...)
		pos = c.LineIndex
		out = append(out, "<<<<<<< "+ourLabel)
		out = append(out, c.Ours...)
		out = append(out, "||||||| base")
		out = append(out, c.Base...)
		out = append(out, "=======")
		out = append(out, c.Theirs...)
		out = append(out, ">>>>>>> "+theirLabel)
	}
	out = append(out, r.Lines[pos:]...)
	return out, nil
}
