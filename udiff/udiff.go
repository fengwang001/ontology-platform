package udiff

import (
	"errors"

	"ontology/hunk"
)

type Patch struct {
	OldName string
	NewName string
	Hunks   []*hunk.Hunk
}

var ErrMalformed = errors.New("udiff: malformed patch")

type LineError struct {
	Line int
	Msg  string
	Op   string
}

func (e *LineError) Error() string { return "" }

func Render(p *Patch) []byte { return nil }

func Parse(b []byte, maxBytes, maxHunks int) (*Patch, error) { return nil, nil }
