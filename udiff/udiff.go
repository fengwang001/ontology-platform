package udiff

import "ontology/hunk"

type Patch struct {
	OldName string
	NewName string
	Hunks   []*hunk.Hunk
}

func Render(p Patch) []byte { return nil }

func Parse(data []byte, maxBytes, maxHunks int) (Patch, error) {
	return Patch{}, nil
}
