// Package api is the public entry point of the in-memory RGA collaborative
// text CRDT. It depends only on doc.
package api

import (
	"errors"
	"fmt"
	"sort"

	"ontology/doc"
	"ontology/rga"
)

// ID is the globally unique element identifier (lamport, replica); zero is ∅.
type ID = rga.ID

// Decidable sentinel errors, re-exported from doc.
var (
	ErrInvalidID      = doc.ErrInvalidID
	ErrDuplicateID    = doc.ErrDuplicateID
	ErrPrevNotFound   = doc.ErrPrevNotFound
	ErrDeleteNotFound = doc.ErrDeleteNotFound
	ErrAlreadyDeleted = doc.ErrAlreadyDeleted
)

// Doc is the replicated document handle.
type Doc struct{ d *doc.Doc }

func New() *Doc                                  { return &Doc{d: doc.New()} }
func (x *Doc) Insert(prev, id ID, ch rune) error { return x.d.Insert(prev, id, ch) }
func (x *Doc) Delete(id ID) error                { return x.d.Delete(id) }
func (x *Doc) Text() string                      { return x.d.Text() }

type op struct {
	id, prev ID
	ch       rune
	del      bool
}

func eid(l int, r string) ID { return ID{Lamport: l, Replica: r} }

func eightOps() []op {
	A, B := "A", "B"
	return []op{
		{id: eid(1, A), ch: 'a'},
		{id: eid(2, A), prev: eid(1, A), ch: 'b'},
		{id: eid(1, B), prev: eid(1, A), ch: 'x'},
		{id: eid(3, A), prev: eid(2, A), ch: 'c'},
		{id: eid(2, B), prev: eid(1, B), ch: 'y'},
		{id: eid(1, A), del: true},
		{id: eid(3, B), prev: eid(1, A), ch: 'z'},
		{id: eid(2, A), del: true},
	}
}

func apply(x *Doc, o op) error {
	if o.del {
		return x.Delete(o.id)
	}
	return x.Insert(o.prev, o.id, o.ch)
}

// naive is the independent batch reference: rebuild adjacency, DFS with
// children sorted by the ID total order, skip tombstones, keep descendants.
func naive(ops []op) string {
	kids := map[ID][]ID{}
	ent := map[ID]op{}
	for _, o := range ops {
		ent[o.id] = o
		if !o.del { // deletes only set the tombstone flag; they create no edge
			kids[o.prev] = append(kids[o.prev], o.id)
		}
	}
	var out []rune
	var walk func(ID)
	walk = func(p ID) {
		cs := append([]ID(nil), kids[p]...)
		sort.Slice(cs, func(i, j int) bool { return rga.Less(cs[i], cs[j]) })
		for _, c := range cs {
			if !ent[c].del {
				out = append(out, ent[c].ch)
			}
			walk(c)
		}
	}
	walk(ID{})
	return string(out)
}

// SelfCheck replays the built-in ops on fresh documents and verifies the
// four invariants: trace/reference, convergence, tombstones, atomicity. It
// does not mutate the receiver, so it is safe with concurrent Text calls.
func (x *Doc) SelfCheck() error {
	ops := eightOps()
	want := []string{"a", "ab", "abx", "abcx", "abcxy", "bcxy", "zbcxy", "zcxy"}
	c := New()
	for i := range ops {
		if err := apply(c, ops[i]); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if c.Text() != want[i] || naive(ops[:i+1]) != want[i] {
			return fmt.Errorf("step %d: %q, want %q", i+1, c.Text(), want[i])
		}
	}
	for n, ord := range [][]int{
		{0, 2, 1, 4, 3, 5, 6, 7},
		{0, 2, 1, 3, 4, 6, 5, 7},
		{0, 2, 1, 4, 3, 6, 5, 7},
	} {
		y := New()
		for _, i := range ord {
			if err := apply(y, ops[i]); err != nil {
				return fmt.Errorf("order %d: %w", n, err)
			}
		}
		if y.Text() != "zcxy" {
			return fmt.Errorf("order %d: %q, want zcxy", n, y.Text())
		}
	}
	return checkRejections()
}

// checkRejections verifies the three distinct decidable failures and that
// rejected ops leave the state untouched while the doc stays usable.
func checkRejections() error {
	y := New()
	if err := y.Insert(ID{}, eid(1, "A"), 'a'); err != nil {
		return err
	}
	cases := []struct {
		err  error
		want error
	}{
		{y.Insert(ID{}, eid(1, "A"), 'q'), ErrDuplicateID},
		{y.Insert(eid(9, "A"), eid(2, "A"), 'q'), ErrPrevNotFound},
		{y.Insert(ID{}, ID{Lamport: 3}, 'q'), ErrInvalidID},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			return fmt.Errorf("reject case %d: %v, want %v", i, c.err, c.want)
		}
	}
	if y.Text() != "a" {
		return fmt.Errorf("rejected ops changed state: %q", y.Text())
	}
	if err := y.Insert(eid(1, "A"), eid(2, "A"), 'b'); err != nil || y.Text() != "ab" {
		return fmt.Errorf("doc unusable after rejections: %q %v", y.Text(), err)
	}
	return nil
}
