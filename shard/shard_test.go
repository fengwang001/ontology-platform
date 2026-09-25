package shard

import (
	"reflect"
	"testing"

	"ontology/route"
)

// 本文件是包内测试，因此能读取非导出计数器 probes；
// 不提供任何导出途径去读它（演示/外部测试均无法获得其数值）。

func TestHashAndHome(t *testing.T) {
	cases := []struct {
		key         string
		sum, h2, h3 int
	}{
		{"a", 97, 1, 1},
		{"b", 98, 0, 2},
		{"c", 99, 1, 0},
		{"d", 100, 0, 1},
		{"e", 101, 1, 2},
	}
	for _, c := range cases {
		if got := route.Hash(c.key); got != c.sum {
			t.Errorf("Hash(%q)=%d want %d", c.key, got, c.sum)
		}
		if got := route.Home(c.key, 2); got != c.h2 {
			t.Errorf("Home(%q,2)=%d want %d", c.key, got, c.h2)
		}
		if got := route.Home(c.key, 3); got != c.h3 {
			t.Errorf("Home(%q,3)=%d want %d", c.key, got, c.h3)
		}
	}
}

type op struct {
	typ      string // "put" | "reb"
	key, val string
	n        int
}

// assertRouted 钉住不变量 1：每个 key 恰在其 home 分区出现一次，无副本。
func assertRouted(t *testing.T, s *Store) {
	t.Helper()
	count := map[string]int{}
	for p, part := range s.parts {
		for k := range part {
			if p != route.Home(k, s.n) {
				t.Errorf("key %q lives in part %d but home is %d", k, p, route.Home(k, s.n))
			}
			count[k]++
		}
	}
	for k, c := range count {
		if c != 1 {
			t.Errorf("key %q appears in %d partitions, want exactly 1", k, c)
		}
	}
}

func TestRouteConsistency(t *testing.T) {
	seq := [][]op{
		{{"put", "a", "A", 0}, {"put", "b", "B", 0}, {"put", "c", "C", 0}, {"put", "d", "D", 0}, {"put", "e", "E", 0}, {"reb", "", "", 3}},
		{{"put", "a", "A", 0}, {"put", "a", "A2", 0}, {"reb", "", "", 1}, {"reb", "", "", 7}, {"reb", "", "", 2}, {"put", "f", "F", 0}},
		{{"reb", "", "", 5}, {"put", "x", "X", 0}, {"reb", "", "", 4}, {"put", "x", "X2", 0}},
	}
	for i, ops := range seq {
		s, err := New(2)
		if err != nil {
			t.Fatalf("seq %d: New: %v", i, err)
		}
		for _, o := range ops {
			switch o.typ {
			case "put":
				s.Put(o.key, o.val)
			case "reb":
				if err := s.Rebalance(o.n); err != nil {
					t.Fatalf("seq %d: Rebalance(%d): %v", i, o.n, err)
				}
			}
			assertRouted(t, s)
		}
	}
}

// naive 把全集按当前 n 重新算术分桶（参照实现）。
func naive(all map[string]string, n int) []map[string]string {
	b := make([]map[string]string, n)
	for i := range b {
		b[i] = map[string]string{}
	}
	for k, v := range all {
		b[route.Home(k, n)][k] = v
	}
	return b
}

func TestNaiveRebuildEquivalence(t *testing.T) {
	seq := []op{
		{"put", "a", "A", 0}, {"put", "b", "B", 0}, {"put", "c", "C", 0},
		{"put", "d", "D", 0}, {"put", "e", "E", 0}, {"reb", "", "", 3},
		{"put", "b", "B2", 0}, {"reb", "", "", 4}, {"put", "g", "G", 0},
	}
	s, _ := New(2)
	all := map[string]string{}
	n := 2
	for _, o := range seq {
		if o.typ == "put" {
			s.Put(o.key, o.val)
			all[o.key] = o.val
		} else {
			n = o.n
			if err := s.Rebalance(n); err != nil {
				t.Fatalf("Rebalance: %v", err)
			}
		}
		if got, want := s.Snapshot(), naive(all, n); !reflect.DeepEqual(got, want) {
			t.Fatalf("after %+v: buckets %v want %v", o, got, want)
		}
	}
}

// TestProbeCountConstantInM 证明定位是算术的：检查分区数恒为 1，
// 不随分区数 m（100..10000）线性增长。
func TestProbeCountConstantInM(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		s, err := New(m)
		if err != nil {
			t.Fatalf("New(%d): %v", m, err)
		}
		s.Put("k", "v")
		if _, ok := s.Get("k"); !ok {
			t.Fatalf("m=%d: Get(k) miss", m)
		}
		if got := s.probes.Load(); got != 1 {
			t.Errorf("m=%d: probes=%d, want constant 1 (no scan)", m, got)
		}
		if _, ok := s.Get("missing"); ok || s.probes.Load() != 1 {
			t.Errorf("m=%d: missing-key probes not constant", m)
		}
	}
}
