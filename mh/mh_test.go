package mh

import (
	"testing"

	"ontology/hash"
)

func mustHashes(t *testing.T, k int) []hash.Hash {
	t.Helper()
	hs := make([]hash.Hash, k)
	for i := 0; i < k; i++ {
		h, err := hash.New(uint64(2*i+1), uint64(3*i+1), 1000003)
		if err != nil {
			t.Fatal(err)
		}
		hs[i] = h
	}
	return hs
}

// 单次 Add 恰好 k 次哈希求值，与集合规模 m 无关（O(k)，非 O(m)）。
func TestAddEvalsIndependentOfSize(t *testing.T) {
	const k = 16
	for _, m := range []int{100, 1000, 5000, 10000} {
		s := New(mustHashes(t, k))
		for x := 0; x < m; x++ {
			before := s.evals
			s.Add(uint64(x))
			if got := s.evals - before; got != k {
				t.Fatalf("m=%d: 单次 Add 求值 %d 次,  want %d", m, got, k)
			}
		}
		if got, want := s.evals, uint64(m*k); got != want {
			t.Fatalf("m=%d: 总求值 %d, want %d", m, got, want)
		}
	}
}

// 空签名全为 MaxUint64；Add 后逐位等于朴素最小值。
func TestSignatureMinSemantics(t *testing.T) {
	hs := mustHashes(t, 4)
	s := New(hs)
	for i, v := range s.Signature() {
		if v != ^uint64(0) {
			t.Fatalf("空签名 sig[%d]=%d, want MaxUint64", i, v)
		}
	}
	for _, x := range []uint64{7, 3, 9} {
		s.Add(x)
	}
	for i, h := range hs {
		want := ^uint64(0)
		for _, x := range []uint64{7, 3, 9} {
			if v := h.Eval(x); v < want {
				want = v
			}
		}
		if got := s.Signature()[i]; got != want {
			t.Fatalf("sig[%d]=%d, want %d", i, got, want)
		}
	}
}
