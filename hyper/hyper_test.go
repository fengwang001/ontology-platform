package hyper

import (
	"errors"
	"testing"

	"ontology/vec"
)

func TestFamilyTable(t *testing.T) {
	const dim, bits, tables = 4, 8, 4
	cases := []struct {
		name      string
		seed      int64
		seed2     int64
		wantEqual bool
	}{
		{"same-seed-identical", 42, 42, true},
		{"diff-seed-differ", 42, 43, false},
		{"large-seed-identical", 1 << 20, 1 << 20, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f1 := NewFamily(dim, bits, tables, tc.seed)
			f2 := NewFamily(dim, bits, tables, tc.seed2)
			data := []vec.Vec{{0, 0, 0, 0}, {1, -2, 3, 0.5}, {-1, 2, -3, 4}}
			anyDiff := false
			for tbi := range f1.Planes {
				for ki := range f1.Planes[tbi] {
					if f1.Planes[tbi][ki][0] != f2.Planes[tbi][ki][0] {
						anyDiff = true
					}
				}
			}
			for ti, x := range data {
				for tb := 0; tb < tables; tb++ {
					s1, err := f1.Signature(tb, x)
					if err != nil {
						t.Fatal(err)
					}
					s2, err := f2.Signature(tb, x)
					if err != nil {
						t.Fatal(err)
					}
					if ti == 0 {
						continue // zero vector always signs to 0
					}
					if (s1 == s2) != tc.wantEqual {
						t.Fatalf("vec %d table %d sig %08b vs %08b", ti, tb, s1, s2)
					}
				}
			}
			if !tc.wantEqual && !anyDiff {
				t.Fatal("different seeds produced identical planes")
			}
			if tc.wantEqual {
				if f1.HashCount != int64(len(data)*tables*bits) {
					t.Fatalf("hash count = %d", f1.HashCount)
				}
				if f1.SignatureMust(0, vec.Vec{0, 0, 0, 0}) != 0 {
					t.Fatal("zero vector must sign to all-zero")
				}
			}
		})
	}
}

func TestZeroPlaneTable(t *testing.T) {
	good := vec.Vec{1, 0}
	cases := []struct {
		name       string
		table, bit int
	}{
		{"table0-bit0", 0, 0},
		{"table1-bit1", 1, 1},
		{"table2-bit0", 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planes := [][]vec.Vec{{good, good}, {good, good}, {good, good}}
			planes[tc.table][tc.bit] = vec.Vec{0, 0}
			_, err := FromPlanes(planes, 1)
			var zp ZeroPlaneError
			if !errors.Is(err, ErrZeroPlane) || !errors.As(err, &zp) {
				t.Fatalf("err=%v", err)
			}
			if zp.Table != tc.table || zp.Bit != tc.bit {
				t.Fatalf("loc=%d,%d want %d,%d", zp.Table, zp.Bit, tc.table, tc.bit)
			}
		})
	}
}

func TestDimErrorTable(t *testing.T) {
	f := NewFamily(3, 4, 2, 7)
	if _, err := f.Signature(0, vec.Vec{1, 2}); !errors.Is(err, vec.ErrDimMismatch) {
		t.Fatalf("err=%v", err)
	}
}

// SignatureMust is a test helper that panics on error.
func (f *Family) SignatureMust(t int, x vec.Vec) uint64 {
	s, err := f.Signature(t, x)
	if err != nil {
		panic(err)
	}
	return s
}
