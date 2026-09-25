package hll

import (
	"fmt"
	"math"
	"testing"

	"ontology/hsh"
)

// Estimate must rely on incrementally maintained Z and V: the number of
// registers it actually reads is always 0, independent of m.
func TestEstimateReadsZero(t *testing.T) {
	for _, p := range []uint{4, 8, 12, 16} {
		t.Run(fmt.Sprintf("p=%d", p), func(t *testing.T) {
			s, err := New(p)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 30000; i++ {
				if err := s.Add(fmt.Sprintf("k-%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Estimate(); err != nil {
				t.Fatal(err)
			}
			if s.reads != 0 {
				t.Fatalf("Estimate read %d registers, want 0 (m=%d)", s.reads, 1<<p)
			}
		})
	}
}

// The eight-key derivation table from NOTES.md, pinned as a test vector.
func TestEightKeyVector(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	want := []struct {
		hash uint64
		j    uint32
		rho  uint8
	}{
		{0x02c0bdbf481420f8, 8, 7},
		{0x3e35b21bfb9b6405, 5, 3},
		{0x7b8855250f3cde02, 2, 2},
		{0x57905f59af4e3d5d, 13, 2},
		{0xafca0c33e25677df, 15, 1},
		{0xd8a688545ff41755, 5, 1},
		{0x73b5d188cc5fb094, 4, 2},
		{0xc90036f097882105, 5, 1},
	}
	s, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range keys {
		h := hsh.Hash(k)
		j, rho := hsh.Bucket(h, 4)
		if h != want[i].hash || j != want[i].j || rho != want[i].rho {
			t.Errorf("key %q: got (%x, %d, %d), want (%x, %d, %d)",
				k, h, j, rho, want[i].hash, want[i].j, want[i].rho)
		}
		if err := s.Add(k); err != nil {
			t.Fatal(err)
		}
	}
	est, err := s.Estimate()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(est-7.520058) > 1e-4 {
		t.Errorf("Estimate = %f, want 7.520058 (linear counting)", est)
	}
}

// Merge of two sketches built from disjoint key sets must equal, bucket by
// bucket, one sketch built from all keys; merge is commutative and idempotent.
func TestMergeConsistency(t *testing.T) {
	for _, p := range []uint{4, 8, 12} {
		t.Run(fmt.Sprintf("p=%d", p), func(t *testing.T) {
			mk := func() *Sketch {
				s, err := New(p)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			a, b, all := mk(), mk(), mk()
			for i := 0; i < 3000; i++ {
				k := fmt.Sprintf("mc-%d", i)
				all.Add(k)
				if i%2 == 0 {
					a.Add(k)
				} else {
					b.Add(k)
				}
			}
			m1, m2 := mk(), mk()
			m1.Merge(a)
			m1.Merge(b)
			m2.Merge(b)
			m2.Merge(a)
			if !m1.Equal(all) {
				t.Error("merge(a,b) != sequential adds")
			}
			if !m2.Equal(all) {
				t.Error("merge not commutative")
			}
			cp := mk()
			cp.Merge(a)
			a.Merge(a)
			if !a.Equal(cp) {
				t.Error("merge not idempotent")
			}
		})
	}
}
