package id

import "ontology/uf"

type Index = int

var (
	ErrBadIndex     = uf.ErrBadIndex
	ErrNegativeSize = uf.ErrNegativeSize
	ErrNilSet       = uf.ErrNilSet
)

type Set = uf.Set

func New(n Index) *Set {
	return uf.New(n)
}
