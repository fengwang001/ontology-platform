package patch

import (
	"ontology/udiff"
)

type Options struct{}

type Store struct{}

func Apply(a []byte, p udiff.Patch, opts Options) ([]byte, error) { return nil, nil }

func Reverse(b []byte, p udiff.Patch, opts Options) ([]byte, error) {
	return nil, nil
}

func NewStore() *Store { return &Store{} }
