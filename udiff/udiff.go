// Package udiff renders and parses unified-diff text.
package udiff

// Patch is one file-to-file unified diff.
type Patch struct {
	OldName    string
	NewName    string
	OldNoNL    bool // old file's final line has no newline
	NewNoNL    bool // new file's final line has no newline
}

// ErrMalformed reports a syntactically invalid patch; Line is 1-based text line.
type MalformedError struct {
	Line int
	Msg  string
}

func (e *MalformedError) Error() string { return "udiff: malformed patch" }

// Render produces canonical unified-diff text.
func Render(p *Patch) []byte { return nil }

// Parse reads one unified diff, reporting the first syntax error with line number.
func Parse(text []byte) (*Patch, error) { return nil, nil }
