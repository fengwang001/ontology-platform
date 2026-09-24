package hyper

import (
	"testing"

	"ontology/vec"
)

func TestFamily(t *testing.T) {
	vecs := []vec.Vec{{1, 2, 3}, {-4, 0.5, 2}, {0, 0, 0}, {9, -1, 1}}

	t.Run("reproducibility", func(t *testing.T) {
		cases := []struct {
			name         string
			seedA, seedB int64
			wantSame     bool
		}{
			{"same seed identical", 11, 11, true},
			{"different seed differs", 11, 12, false},
		}
		for _, tc := range cases {
			a, b := New(tc.seedA, 3, 16), New(tc.seedB, 3, 16)
			same := true
			for _, v := range vecs {
				if a.Signature(v) != b.Signature(v) {
					same = false
				}
			}
			if same != tc.wantSame {
				t.Errorf("%s: same=%v want %v", tc.name, same, tc.wantSame)
			}
		}
	})

	t.Run("hash count exact", func(t *testing.T) {
		cases := []struct{ nVec, bits int }{{4, 16}, {7, 5}, {1, 1}}
		for _, tc := range cases {
			f := New(3, 3, tc.bits)
			f.ResetComputed()
			for i := 0; i < tc.nVec; i++ {
				f.Signature(vecs[i%len(vecs)])
			}
			if got := f.Computed(); got != uint64(tc.nVec*tc.bits) {
				t.Errorf("nVec=%d bits=%d: computed=%d want %d",
					tc.nVec, tc.bits, got, tc.nVec*tc.bits)
			}
		}
	})

	t.Run("zero vector convention", func(t *testing.T) {
		for _, bits := range []int{1, 8, 64} {
			f := New(5, 3, bits)
			if got := f.Signature(vec.Vec{0, 0, 0}); got != 1<<uint(bits)-1 {
				t.Errorf("bits=%d: zero vector sig=%b want all ones", bits, got)
			}
		}
	})
}
