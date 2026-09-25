package mh

import (
	"math"
	"testing"

	"ontology/hash"
)

func testHashes(t *testing.T) []hash.Hash {
	t.Helper()
	hs := make([]hash.Hash, 4)
	for i, p := range [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}} {
		h, err := hash.New(p[0], p[1], p[2])
		if err != nil {
			t.Fatal(err)
		}
		hs[i] = h
	}
	return hs
}

func TestSignatureCorrect(t *testing.T) {
	inf := uint64(math.MaxUint64)
	cases := []struct {
		name string
		xs   []uint64
		want []uint64
	}{
		{"empty", nil, []uint64{inf, inf, inf, inf}},
		{"A", []uint64{1, 4, 7}, []uint64{0, 0, 2, 7}},
		{"B", []uint64{1, 4, 8, 9}, []uint64{0, 2, 0, 3}},
		{"single8", []uint64{8}, []uint64{8, 3, 0, 3}},
		{"dup", []uint64{4, 4, 1}, []uint64{0, 2, 2, 8}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hs := testHashes(t)
			s, err := New(hs)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range c.xs {
				s.Add(x)
			}
			got := s.Signature()
			// 与测试内朴素重算逐位对比，并核对预设值。
			for i, h := range hs {
				want := inf
				for _, x := range c.xs {
					if v := h.Eval(x); v < want {
						want = v
					}
				}
				if got[i] != want || got[i] != c.want[i] {
					t.Fatalf("sig[%d]=%d, naive=%d, table=%d", i, got[i], want, c.want[i])
				}
			}
			if got2 := s.Signature(); got2[0] == got[0] && &got2[0] == &got[0] {
				t.Fatal("Signature must return a copy")
			}
		})
	}
}

func TestAddEvalsConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		hs := testHashes(t)
		s, err := New(hs)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			before := s.evals
			s.Add(uint64(i))
			if d := s.evals - before; d != len(hs) {
				t.Fatalf("m=%d: one Add did %d evals, want k=%d", m, d, len(hs))
			}
		}
		if s.evals != m*len(hs) {
			t.Fatalf("m=%d: total evals=%d, want %d", m, s.evals, m*len(hs))
		}
	}
}

func TestNewRejectsBadK(t *testing.T) {
	if s, err := New(nil); s != nil || err != ErrBadK {
		t.Fatalf("New(nil) = %v, %v", s, err)
	}
}
