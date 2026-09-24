package hyper

import (
	"errors"
	"testing"

	"ontology/vec"
)

func TestSignaturesReproducibility(t *testing.T) {
	dim, L, b := 8, 4, 8
	xs := make([]vec.Vec, 50)
	for i := range xs {
		xs[i] = make(vec.Vec, dim)
		for j := range xs[i] {
			xs[i][j] = float64((i*7 + j*3) % 11)
		}
	}
	f1, _ := New(dim, L, b, 42)
	f2, _ := New(dim, L, b, 42)
	f3, _ := New(dim, L, b, 43)
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"same seed byte-identical signatures", func(t *testing.T) {
			for _, x := range xs {
				for tb := 0; tb < L; tb++ {
					s1, _ := f1.Signature(tb, x)
					s2, _ := f2.Signature(tb, x)
					if s1 != s2 {
						t.Fatalf("signature mismatch %x %x", s1, s2)
					}
				}
			}
		}},
		{"different seed differs somewhere", func(t *testing.T) {
			diff := false
			for _, x := range xs {
				for tb := 0; tb < L; tb++ {
					s1, _ := f1.Signature(tb, x)
					s3, _ := f3.Signature(tb, x)
					if s1 != s3 {
						diff = true
					}
				}
			}
			if !diff {
				t.Fatal("expected differing signatures across seeds")
			}
		}},
		{"zero vector maps to all-zero signature", func(t *testing.T) {
			z := make(vec.Vec, dim)
			s, err := f1.Signature(0, z)
			if err != nil || s != 0 {
				t.Fatalf("zero signature=%x err=%v", s, err)
			}
		}},
		{"dimension mismatch classified", func(t *testing.T) {
			_, err := f1.Signature(0, vec.Vec{1, 2})
			if !errors.Is(err, vec.ErrDimMismatch) {
				t.Fatalf("want dim mismatch, got %v", err)
			}
		}},
		{"degenerate zero normal detected with table/bit", func(t *testing.T) {
			ns := f1.Normals
			ns[2][3] = make(vec.Vec, dim)
			_, err := FromNormals(ns)
			if !errors.Is(err, ErrDegenerateHyperplane) {
				t.Fatalf("want degenerate, got %v", err)
			}
			tb, bi, ok := TableBit(err)
			if !ok || tb != 2 || bi != 3 {
				t.Fatalf("TableBit=%d,%d,%v", tb, bi, ok)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}
