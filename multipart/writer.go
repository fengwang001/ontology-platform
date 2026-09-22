package multipart

import "ontology/coalesce"

type Part struct {
	Range coalesce.Range
	Data  []byte
}

type Builder struct{}

func New(totalLength int64) *Builder { return &Builder{} }

func (b *Builder) Boundary() string { return "" }

func (b *Builder) Build(parts []Part) ([]byte, error) {
	_ = parts
	return nil, nil
}

