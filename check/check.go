// Package check 提供朴素有序参照与跳表的全部测试。
package check

import (
	"errors"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"

	"ontology/skip"
)

// Ref 是朴素有序参照：去重的有序切片。
type Ref struct{ keys []int }

func (r *Ref) Insert(k int) {
	i := sort.SearchInts(r.keys, k)
	if i == len(r.keys) || r.keys[i] != k {
		r.keys = slices.Insert(r.keys, i, k)
	}
}

func (r *Ref) Delete(k int) {
	if i := sort.SearchInts(r.keys, k); i < len(r.keys) && r.keys[i] == k {
		r.keys = slices.Delete(r.keys, i, i+1)
	}
}

func (r *Ref) Range(lo, hi int) []int {
	a, b := sort.SearchInts(r.keys, lo), sort.SearchInts(r.keys, hi)
	return slices.Clone(r.keys[a:b])
}

func (r *Ref) Len() int { return len(r.keys) }

func newRef(keys ...int) *Ref {
	r := &Ref{}
	for _, k := range keys {
		r.Insert(k)
	}
	return r
}

func shuffled(n int, seed uint64) []int { return rand.New(rand.NewPCG(seed, 1)).Perm(n) }

func build(seed uint64, keys []int) *skip.List[int] {
	l := skip.New[int](seed)
	for _, k := range keys {
		_ = l.Insert(k, k)
	}
	return l
}

func findOK(l *skip.List[int], k int) bool {
	v, ok := l.Find(k)
	return ok && v == k
}

func mustErr(t *testing.T, err, sentinel error) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("want %v, got %v", sentinel, err)
	}
}
