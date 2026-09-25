package main

import (
	"errors"
	"fmt"

	"ontology/check"
	"ontology/id"
	"ontology/uf"
)

type verdict struct {
	name string
	ok   bool
	err  error
}

func chainHops(n int) (rankHops int, err error) {
	d, e := uf.New(n)
	if e != nil {
		return 0, e
	}
	for i := 1; i < n; i++ {
		if _, e := d.Union(i, i-1); e != nil {
			return 0, e
		}
	}
	if _, e := d.Find(0); e != nil {
		return 0, e
	}
	return d.LastFindHops(), nil
}

func main() {
	n := 1 << 12
	d, err := uf.New(n)
	if err != nil {
		panic(err)
	}
	for i := 1; i < n; i++ {
		merged, e := d.Union(i, i-1)
		if e != nil || !merged {
			panic("union failed")
		}
	}
	conn, _ := d.Connected(0, n-1)
	s, _ := id.New(3)
	sUnion, _ := s.Union(id.ID(0), id.ID(2))
	sConn, _ := s.Connected(id.ID(0), id.ID(2))
	_, badIdxErr := d.Find(n + 1)
	empty, _ := uf.New(0)
	_, emptyErr := empty.Find(0)
	rankHops, _ := chainHops(1 << 16)
	ref := check.NewRef(4)
	ref.Union(0, 1)
	checks := []verdict{
		{"skeleton runs", true, nil},
		{"uf chain all connected", conn, nil},
		{"uf count equals 1", d.Count() == 1, nil},
		{"uf rank height <= log2(n)", rankHops <= 16, nil},
		{"uf Find out of range -> ErrBadIndex", errors.Is(badIdxErr, uf.ErrBadIndex), badIdxErr},
		{"id typed union+connected", sUnion && sConn && s.Count() == 2, nil},
		{"id forwards sentinel error", errors.Is(func() error {
			_, e := s.Find(id.ID(9))
			return e
		}(), id.ErrBadIndex), nil},
		{"check BFS ref agrees", ref.Count() == 3 && ref.Connected(0, 1) &&
			errors.Is(emptyErr, uf.ErrEmptySet), emptyErr},
	}
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		fmt.Println("FAIL demo")
		return
	}
}
