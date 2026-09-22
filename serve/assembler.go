package serve

import (
	"context"
	"errors"

	"ontology/source"
)

type Limits struct {
	MaxRanges      int
	MaxResponse    int64
	BoundaryTries  int
}

type Assembler struct {
	source source.Source
}

func New(src source.Source, limits Limits) *Assembler {
	return &Assembler{source: src}
}

func (a *Assembler) Build(ctx context.Context, rangeHeader string) error {
	_ = rangeHeader
	return nil
}

func (a *Assembler) Write(p []byte) (int, error) { return len(p), nil }

var (
	ErrTooManyRanges   = errors.New("serve: too many ranges")
	ErrResponseTooLarge = errors.New("serve: response too large")
	ErrBoundary        = errors.New("serve: cannot select safe boundary")
)

