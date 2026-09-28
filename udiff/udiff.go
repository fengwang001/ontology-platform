package udiff

import "ontology/hunk"

type File struct {
	Name  string
	Hunks []hunk.Hunk
}

type Limits struct {
	MaxBytes int
	MaxHunks int
}

func Render(p File) []byte { return nil }

func Parse(data []byte, lim Limits) (File, error) { return File{}, nil }
