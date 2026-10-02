package ontology

import (
	"errors"
	"testing"
)

func TestErrorKindsAndPrecedence(t *testing.T) {
	invalidCases := []struct {
		name string
		run  func() error
	}{
		{"constructor depth", func() error { _, err := New(0, 4); return err }},
		{"constructor entries", func() error { _, err := New(4, 65); return err }},
		{"add empty id", func() error { a := mustNewACL(t, 1, 1); return a.AddNode(nil, []byte(RootID), true) }},
		{"set empty node", func() error { a := mustNewACL(t, 1, 1); return a.SetACL(nil, nil, false) }},
		{"set invalid principal", func() error {
			a := mustNewACL(t, 1, 1)
			return a.SetACL([]byte(RootID), []ACE{{Principal: nil, Mask: 1}}, false)
		}},
		{"set invalid mask", func() error {
			a := mustNewACL(t, 1, 1)
			return a.SetACL([]byte(RootID), []ACE{{Principal: []byte("p"), Mask: 0}}, false)
		}},
		{"set invalid flags", func() error {
			a := mustNewACL(t, 1, 1)
			return a.SetACL([]byte(RootID), []ACE{{Principal: []byte("p"), Mask: 1, Flags: 16}}, false)
		}},
		{"set io without inheritance", func() error {
			a := mustNewACL(t, 1, 1)
			return a.SetACL([]byte(RootID), []ACE{{Principal: []byte("p"), Mask: 1, Flags: FlagIO}}, false)
		}},
		{"move empty node", func() error { a := mustNewACL(t, 1, 1); return a.Move(nil, []byte(RootID)) }},
		{"eval empty token", func() error {
			a := mustNewACL(t, 1, 1)
			_, err := a.Eval(nil, []byte(RootID), 1)
			return err
		}},
		{"eval empty token principal", func() error {
			a := mustNewACL(t, 1, 1)
			_, err := a.Eval([][]byte{{}, []byte("p")}, []byte(RootID), 1)
			return err
		}},
		{"eval zero request", func() error {
			a := mustNewACL(t, 1, 1)
			_, err := a.Eval([][]byte{[]byte("p")}, []byte(RootID), 0)
			return err
		}},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.run(), ErrInvalidArgument) {
				t.Fatalf("want invalid argument")
			}
		})
	}

	a := mustNewACL(t, 2, 1)
	if err := a.AddNode([]byte(RootID), []byte(RootID), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("slash id = %v, want conflict", err)
	}
	mustAdd(t, a, "d", RootID, true)
	mustAdd(t, a, "f", "d", false)

	if err := a.AddNode([]byte("x"), []byte("missing"), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Add parent = %v", err)
	}
	if err := a.AddNode([]byte("d"), []byte("missing"), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent precedes duplicate = %v", err)
	}
	if err := a.AddNode([]byte("x"), []byte("f"), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("object parent = %v", err)
	}
	if err := a.Move([]byte("missing"), []byte("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Move checks node first = %v", err)
	}
	if err := a.Move([]byte("f"), []byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing new parent = %v", err)
	}
	if err := a.Move([]byte("f"), []byte("d")); !errors.Is(err, ErrConflict) {
		t.Fatalf("same parent = %v", err)
	}
}
