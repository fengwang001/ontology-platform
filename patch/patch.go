package patch

import (
	"ontology/udiff"
)

type Options struct {
	Fuzz       int
	MaxBytes   int
	MaxHunks   int
	MaxEdit    int
	Reversed   bool
	CheckNames bool
	OldName    string
	NewName    string
}

func Apply(data []byte, text []byte, opts Options) ([]byte, error) {
	_, err := udiff.Parse(data, 0, 0)
	return nil, err
}
