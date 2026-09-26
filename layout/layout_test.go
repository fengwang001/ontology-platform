package layout

import (
	"errors"
	"fmt"
	"testing"
)

func TestOffsetsPacked(t *testing.T) {
	cases := []struct {
		name  string
		specs []Field
		size  int
	}{
		{"empty", nil, 0},
		{"one", []Field{{Name: "a", Width: 1}}, 1},
		{"mixed", []Field{
			{Name: "a", Width: 8}, {Name: "b", Width: 1},
			{Name: "c", Width: 2}, {Name: "d", Width: 4},
		}, 15},
		{"canonical", []Field{
			{Name: "id", Width: 2}, {Name: "flags", Width: 1},
			{Name: "count", Width: 4}, {Name: "score", Width: 2, Signed: true},
		}, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSchema(tc.specs)
			if err != nil {
				t.Fatalf("NewSchema: %v", err)
			}
			if s.Size() != tc.size {
				t.Fatalf("size = %d, want %d", s.Size(), tc.size)
			}
			// Offsets are the running sum; intervals tile [0,size) with
			// neither overlap nor gap.
			off := 0
			for _, f := range s.Fields() {
				if f.Offset != off {
					t.Fatalf("%s offset = %d, want %d", f.Name, f.Offset, off)
				}
				off += f.Width
			}
			if off != tc.size {
				t.Fatalf("coverage = %d, want %d", off, tc.size)
			}
		})
	}
}

func TestSchemaConstructionErrors(t *testing.T) {
	cases := []struct {
		name string
		spec []Field
		want error
	}{
		{"empty name", []Field{{Name: "", Width: 1}}, ErrEmptyFieldName},
		{"bad width 3", []Field{{Name: "a", Width: 3}}, ErrInvalidWidth},
		{"bad width 0", []Field{{Name: "a", Width: 0}}, ErrInvalidWidth},
		{"duplicate", []Field{{Name: "a", Width: 1}, {Name: "a", Width: 2}}, ErrDuplicateField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSchema(tc.spec); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestLookupConstantProbes reads the unexported probe counter directly
// (white-box). Locating the last field by name performs a number of
// comparisons bounded by a constant independent of the schema size m.
func TestLookupConstantProbes(t *testing.T) {
	const bound = 2 // hash-directed lookup: a single key compare, give slack
	for _, m := range []int{100, 1000, 10000} {
		specs := make([]Field, m)
		for i := range specs {
			specs[i] = Field{Name: fmt.Sprintf("f%d", i), Width: []int{1, 2, 4, 8}[i%4]}
		}
		s, err := NewSchema(specs)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		for _, target := range []string{"f0", fmt.Sprintf("f%d", m-1)} {
			s.probeCount = 0
			f, ok := s.FieldByName(target)
			if !ok || f.Name != target {
				t.Fatalf("m=%d: lookup %s failed", m, target)
			}
			if s.probeCount > bound {
				t.Fatalf("m=%d target=%s: probes=%d > %d (linear scan?)", m, target, s.probeCount, bound)
			}
		}
		if _, ok := s.FieldByName("missing"); ok {
			t.Fatalf("missing field reported present")
		}
	}
}
