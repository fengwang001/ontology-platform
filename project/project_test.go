package project

import (
	"bytes"
	"encoding/gob"
	"reflect"
	"testing"
)

func TestProjectRemovesInvisible(t *testing.T) {
	cases := []struct {
		name string
		obj  Object
		vis  func(string) bool
		want Object
	}{
		{"remove one", Object{"a": 1, "b": 2}, func(k string) bool { return k == "a" }, Object{"a": 1}},
		{"all invisible", Object{"a": 1}, func(string) bool { return false }, Object{}},
		{"all visible", Object{"a": 1, "b": 2}, func(string) bool { return true }, Object{"a": 1, "b": 2}},
		{"zero value kept, missing absent",
			Object{"a": "", "b": 0}, func(k string) bool { return k == "a" }, Object{"a": ""}},
		{"nil value kept when visible",
			Object{"a": nil, "b": 1}, func(k string) bool { return k == "a" }, Object{"a": nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProjector()
			got := p.Project(tc.obj, tc.vis)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if p.removed != len(tc.obj)-len(tc.want) {
				t.Fatalf("removed = %d, want %d", p.removed, len(tc.obj)-len(tc.want))
			}
		})
	}
}

func TestProjectDeterministic(t *testing.T) {
	obj := Object{"z": 1, "a": 2, "m": 3, "b": 4, "secret": 5}
	vis := func(k string) bool { return k != "secret" }
	want := Object{"z": 1, "a": 2, "m": 3, "b": 4}
	var first []byte
	for i := 0; i < 50; i++ {
		p := NewProjector()
		got := p.Project(obj, vis)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d: got %v", i, got)
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(got); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = buf.Bytes()
		} else if !bytes.Equal(first, buf.Bytes()) {
			t.Fatalf("iter %d: projection bytes differ", i)
		}
	}
}

func TestProjectDoesNotMutateInput(t *testing.T) {
	obj := Object{"a": 1, "secret": 2}
	p := NewProjector()
	_ = p.Project(obj, func(string) bool { return false })
	if len(obj) != 2 {
		t.Fatalf("input mutated: %v", obj)
	}
}
