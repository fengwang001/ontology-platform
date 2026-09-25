package id

import "ontology/uf"

type Index int

var (
	ErrBadIndex = uf.ErrBadIndex
	ErrBadSize  = uf.ErrBadSize
	ErrTooLarge = uf.ErrTooLarge
)

type Set struct {
	dsu *uf.DSU
}

func NewSet(n int) (*Set, error) {
	dsu, err := uf.New(n)
	if err != nil {
		return nil, err
	}
	return &Set{dsu: dsu}, nil
}

func (s *Set) Find(x Index) (Index, error) {
	root, err := s.dsu.Find(int(x))
	return Index(root), err
}

func (s *Set) Union(x, y Index) (bool, error) {
	return s.dsu.Union(int(x), int(y))
}

func (s *Set) Connected(x, y Index) (bool, error) {
	return s.dsu.Connected(int(x), int(y))
}

func (s *Set) Count() int {
	return s.dsu.Count()
}

func (s *Set) LastFindHops() int {
	return s.dsu.LastFindHops()
}
