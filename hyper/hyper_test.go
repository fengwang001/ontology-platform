package hyper

import (
	"errors"
	"testing"

	"ontology/vec"
)

func TestReproduciblePlanes(t *testing.T) {
	dim, bits, tables := 5, 8, 3
	f1 := NewFamily(dim, bits, tables, 42)
	f2 := NewFamily(dim, bits, tables, 42)
	if len(f1.planes) != len(f2.planes) {
		t.Fatalf("planes len %d != %d", len(f1.planes), len(f2.planes))
	}
	for i := range f1.planes {
		if f1.planes[i] != f2.planes[i] {
			t.Fatalf("plane[%d] differs under same seed: %v != %v",
				i, f1.planes[i], f2.planes[i])
		}
	}
}

func TestSignatureTable(t *testing.T) {
	f := NewFamily(4, 6, 2, 7)
	zero := vec.Vector{0, 0, 0, 0}
	diff := NewFamily(4, 6, 2, 99)

	tests := []struct {
		name      string
		x         vec.Vector
		wantErr   error
		checkBits func(t *testing.T, s Signature)
	}{
		{
			name: "zerovector-all-ones", x: zero,
			checkBits: func(t *testing.T, s Signature) {
				if s != Signature(1<<6)-1 {
					t.Fatalf("zero vector signature=%b want all ones", s)
				}
			},
		},
		{
			name: "same-seed-identical", x: vec.Vector{1, -2, 3, -4},
			checkBits: func(t *testing.T, s Signature) {
				s2, _ := NewFamily(4, 6, 2, 7).Sign(vec.Vector{1, -2, 3, -4}, 0)
				if s != s2 {
					t.Fatalf("signature differs under same seed: %b != %b", s, s2)
				}
			},
		},
		{
			name: "diff-seed-differs", x: vec.Vector{0.5, 0.5, -0.5, -0.5},
			checkBits: func(t *testing.T, s Signature) {
				s2, _ := diff.Sign(vec.Vector{0.5, 0.5, -0.5, -0.5}, 0)
				if s == s2 {
					t.Fatalf("signature identical across seeds: %b", s)
				}
			},
		},
		{name: "dim-mismatch", x: vec.Vector{1, 2}, wantErr: vec.ErrDimMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := f.Sign(tt.x, 0)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v want %v", err, tt.wantErr)
			}
			if err == nil {
				if _, err := f.Signs(tt.x); err != nil {
					t.Fatalf("Signs: %v", err)
				}
				if tt.checkBits != nil {
					tt.checkBits(t, s)
				}
			}
		})
	}
}

func TestDegenerate(t *testing.T) {
	f := NewFamily(3, 4, 2, 1)
	if f.IsDegenerate(1, 2) {
		t.Fatalf("random plane should not be all-zero")
	}
	off := (1*f.Bits + 2) * f.Dim
	for d := 0; d < f.Dim; d++ {
		f.planes[off+d] = 0
	}
	if !f.IsDegenerate(1, 2) {
		t.Fatalf("zeroed plane must be detected as degenerate")
	}
}
