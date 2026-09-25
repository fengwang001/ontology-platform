package udiff

import (
	"ontology/hunk"
	"ontology/lines"
)

type Options struct{}

type Patch struct{}

func Render(a, b []byte, hs []hunk.Hunk, opts Options) []byte { return nil }

func Parse(data []byte, limitBytes, limitHunks int) (Patch, error) {
	return Patch{}, nil
}

func (p Patch) Hunks() []hunk.Hunk { return nil }

func (p Patch) Lines() ([]lines.Line, []lines.Line) { return nil, nil }
