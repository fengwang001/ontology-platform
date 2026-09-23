package delta

import (
	"errors"
	"testing"

	"ontology/doc"
)

func TestBuild(t *testing.T) {
	anc := doc.Set{
		"r1": {"a": doc.Number(1), "b": doc.String("x"), "c": doc.Number(3)},
		"del": {"a": doc.Number(9)},
	}
	side := doc.Set{
		"r1":  {"a": doc.Number(2), "b": doc.String("x"), "c": doc.Number(3), "n": doc.String("new")},
		"add": {"z": doc.String("")},
	}
	d, err := Build(anc, side)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key  string
		kind Kind
		op   Op
		fld  string
	}{
		{"r1", KindModified, OpChanged, "a"},
		{"r1", KindModified, OpAdded, "n"},
		{"del", KindDeleted, 0, ""},
		{"add", KindAdded, 0, ""},
	}
	byKey := map[string]*Entry{}
	for _, e := range d.Entries() {
		byKey[e.Key] = e
	}
	for _, c := range cases {
		e := byKey[c.key]
		if e == nil || e.Kind != c.kind {
			t.Fatalf("%s: got %+v want kind %v", c.key, e, c.kind)
		}
		if c.fld != "" {
			fc, ok := e.Fields[c.fld]
			if !ok || fc.Op != c.op {
				t.Fatalf("field %s: %+v op %v", c.fld, fc, c.op)
			}
		}
	}
	if _, ok := byKey["r1"].Fields["c"]; ok {
		t.Fatal("unchanged field must not appear")
	}
	if _, err := Build(doc.Set{}, doc.Set{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateContradiction(t *testing.T) {
	cases := []struct {
		name    string
		entries []*Entry
		wantKey string
		wantErr bool
	}{
		{"delete then modify", []*Entry{
			{Key: "k", Kind: KindDeleted},
			{Key: "k", Kind: KindModified},
		}, "k", true},
		{"modify then delete", []*Entry{
			{Key: "k", Kind: KindModified},
			{Key: "k", Kind: KindDeleted},
		}, "k", true},
		{"consistent", []*Entry{
			{Key: "a", Kind: KindAdded},
			{Key: "b", Kind: KindDeleted},
		}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.entries)
			if c.wantErr {
				var ce *ContradictionError
				if !errors.As(err, &ce) || ce.Key != c.wantKey {
					t.Fatalf("err=%v want ContradictionError key %s", err, c.wantKey)
				}
				if !errors.Is(err, ErrContradiction) {
					t.Fatal("errors.Is ErrContradiction failed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
