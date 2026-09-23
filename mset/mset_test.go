package mset_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"ontology/key"
	"ontology/list"
	"ontology/mset"
	"ontology/rank"
)

func build(t *testing.T, keys []key.Key) *mset.Mset {
	m := mset.New(32, 1<<20)
	for _, k := range keys {
		if err := m.Insert(k); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestOrderIndependentStructure(t *testing.T) {
	sets := [][]key.Key{
		{"a", "b", "c", "d", "e", "b", "d", "b"},
		{"x", "y", "x", "z", "x"},
	}
	for _, keys := range sets {
		ref := build(t, keys)
		for shift := range len(keys) {
			perm := append(slices.Clone(keys[shift:]), keys[:shift]...)
			got := build(t, perm)
			if !ref.SameStructure(got) {
				t.Fatalf("%v shift %d: structure differs", keys, shift)
			}
			if err := got.Check(); err != nil {
				t.Fatalf("%v shift %d: %v", keys, shift, err)
			}
		}
	}
}

func TestRankConsistency(t *testing.T) {
	m := build(t, []key.Key{"a", "b", "b", "c", "d", "d", "d", "e"})
	for i := 0; i < m.Len(); i++ {
		k, err := m.At(i)
		back, _ := m.At(m.RankOf(k))
		if err != nil || m.RankOf(k) > i || back != k {
			t.Fatalf("i=%d key=%v err=%v rank=%d back=%v", i, k, err, m.RankOf(k), back)
		}
	}
	ranges := []struct {
		lo, hi key.Key
		want   int
	}{
		{"a", "e", 7}, {"b", "d", 3}, {"aa", "dz", 6}, {"c", "c", 0}, {"0", "z", 8},
	}
	for _, c := range ranges {
		got, err := m.Range(c.lo, c.hi)
		if diff := m.RankOf(c.hi) - m.RankOf(c.lo); err != nil || got != diff || got != c.want {
			t.Errorf("Range(%q,%q)=%d,%v want %d", c.lo, c.hi, got, err, c.want)
		}
	}
}

func TestDuplicateSemantics(t *testing.T) {
	cases := []struct {
		keys          []key.Key
		k             key.Key
		rank, n, left int
	}{
		{[]key.Key{"x", "dup", "z", "dup", "dup"}, "dup", 0, 3, 1},
		{[]key.Key{"a", "b", "b"}, "b", 1, 2, 0},
		{[]key.Key{"a"}, "zz", 1, 0, 0},
	}
	for _, c := range cases {
		m := build(t, c.keys)
		if m.RankOf(c.k) != c.rank || m.Count(c.k) != c.n {
			t.Errorf("%q: rank=%d count=%d, want %d/%d", c.k, m.RankOf(c.k), m.Count(c.k), c.rank, c.n)
		}
		_ = m.Delete(c.k)
		_ = m.Delete(c.k)
		if n := m.Count(c.k); n != c.left {
			t.Errorf("after deletes Count(%q)=%d want %d", c.k, n, c.left)
		}
	}
}

func TestSpansExactAfterWrites(t *testing.T) {
	keys := make([]key.Key, 500)
	for i := range keys {
		keys[i] = key.Key(fmt.Sprintf("k%04d", (i*137)%500))
	}
	m := build(t, keys)
	for i := 0; i < 500; i += 2 {
		if err := m.Delete(key.Key(fmt.Sprintf("k%04d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 250 {
		t.Fatalf("Len()=%d want 250", m.Len())
	}
}

func TestErrorsAndLimits(t *testing.T) {
	m := build(t, []key.Key{"a", "b", "c"})
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"at-range", func() error { _, e := m.At(-1); return e }, rank.ErrOutOfRange},
		{"bad-range", func() error { _, e := m.Range("c", "a"); return e }, rank.ErrBadRange},
		{"delete-missing", func() error { return m.Delete("zz") }, list.ErrNotFound},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	if err := m.Check(); err != nil || m.Len() != 3 {
		t.Fatalf("failed ops mutated structure: %v len=%d", err, m.Len())
	}
	full := mset.New(4, 2)
	_ = full.Insert("a")
	_ = full.Insert("b")
	if err := full.Insert("c"); !errors.Is(err, list.ErrFull) {
		t.Fatalf("got %v want ErrFull", err)
	}
	_ = full.Delete("a")
	if err := full.Insert("c"); err != nil {
		t.Fatalf("unusable after ErrFull: %v", err)
	}
	// deterministic levels: "t0" -> 1, "t1" -> 2
	tall, short := key.Key("t1"), key.Key("t0")
	lm := mset.New(1, 100)
	if err := lm.Insert(tall); !errors.Is(err, list.ErrMaxLevel) {
		t.Fatalf("got %v want ErrMaxLevel", err)
	}
	if err := lm.Insert(short); err != nil {
		t.Fatalf("unusable after ErrMaxLevel: %v", err)
	}
}
