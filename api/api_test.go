package api_test

import (
	"errors"
	"fmt"
	"ontology/api"
	"sync"
	"testing"
)

func checkTiers(t *testing.T, s *api.Store, hotCap, coldCap, nAll int) {
	hot, cold := s.HotKeys(), s.ColdKeys()
	seen := map[string]bool{}
	for _, k := range append(hot, cold...) {
		if seen[k] {
			t.Fatalf("热冷交集非空: %s", k)
		}
		seen[k] = true
	}
	if len(hot) > hotCap || len(cold) > coldCap || len(seen) != nAll {
		t.Fatalf("分层不一致: hot=%d cold=%d all=%d want %d", len(hot), len(cold), len(seen), nAll)
	}
}
func seq(t *testing.T, s *api.Store, hot, cold, keys, ops int, m map[string]int64, each func()) {
	seed := uint32(ops*2654435761 + keys*7919)
	next := func() uint32 { seed = seed*1664525 + 1013904223; return seed >> 16 }
	for i := 0; i < ops; i++ {
		k := fmt.Sprintf("k%d", next()%uint32(keys))
		if next()%2 == 0 {
			v := int64(next() % 1000)
			if e := s.Put(k, v); e == nil {
				m[k] = v
			} else if !errors.Is(e, api.ErrColdFull) {
				t.Fatalf("op%d: %v", i, e)
			}
		} else {
			got, e := s.Get(k)
			if want, ok := m[k]; (!ok && !errors.Is(e, api.ErrNotFound)) || (ok && (e != nil || got != want)) {
				t.Fatalf("op%d: got (%d,%v) want (%d,%v)", i, got, e, want, ok)
			}
		}
		each()
	}
}
func parallel(n int, f func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); f(i) }(i)
	}
	wg.Wait()
}
func TestLRUOrder(t *testing.T) {
	s, _ := api.New(2, 100)
	ops := []string{"put A 1", "put B 2", "get A 1", "put C 3", "get B 2", "put D 4", "get A 1", "put E 5"}
	wantHot := []string{"[A]", "[B A]", "[A B]", "[C A]", "[B C]", "[D B]", "[A D]", "[E A]"}
	wantCold := []string{"[]", "[]", "[]", "[B]", "[A]", "[A C]", "[B C]", "[B C D]"}
	for i, op := range ops {
		var kind, key string
		var val, got int64
		fmt.Sscanf(op, "%s %s %d", &kind, &key, &val)
		e := error(nil)
		if kind == "put" {
			e = s.Put(key, val)
			val = 0
		} else {
			got, e = s.Get(key)
		}
		if e != nil || got != val || fmt.Sprint(s.HotKeys()) != wantHot[i] || fmt.Sprint(s.ColdKeys()) != wantCold[i] {
			t.Fatalf("第%d步: hot=%v cold=%v get=%d err=%v", i+1, s.HotKeys(), s.ColdKeys(), got, e)
		}
	}
}
func TestNaiveConsistency(t *testing.T) {
	for _, c := range []struct{ hot, cold, keys, ops int }{{2, 100, 5, 300}, {1, 2, 4, 400}} {
		s, _ := api.New(c.hot, c.cold)
		m := map[string]int64{}
		seq(t, s, c.hot, c.cold, c.keys, c.ops, m, func() {})
		for k, want := range m {
			if got, ok := s.Value(k); !ok || got != want {
				t.Fatalf("%+v 值不一致 %s", c, k)
			}
		}
	}
}
func TestTierInvariant(t *testing.T) {
	for _, c := range []struct{ hot, cold, keys, ops int }{{2, 3, 6, 200}, {1, 1, 3, 150}} {
		s, _ := api.New(c.hot, c.cold)
		m := map[string]int64{}
		seq(t, s, c.hot, c.cold, c.keys, c.ops, m, func() { checkTiers(t, s, c.hot, c.cold, len(m)) })
	}
}
func TestFailureNoTrace(t *testing.T) {
	if errors.Is(api.ErrBadCap, api.ErrNotFound) || errors.Is(api.ErrEmptyKey, api.ErrColdFull) {
		t.Fatal("哨兵错误不可区分")
	}
	for _, caps := range [][2]int{{0, 1}, {1, -1}} {
		if _, e := api.New(caps[0], caps[1]); !errors.Is(e, api.ErrBadCap) {
			t.Fatalf("%v 应 ErrBadCap", caps)
		}
	}
	s, _ := api.New(1, 1)
	_ = s.Put("a", 1)
	_ = s.Put("b", 2)
	get := func(k string) error { _, e := s.Get(k); return e }
	before := fmt.Sprint(s.HotKeys(), s.ColdKeys())
	bads := []error{s.Put("c", 3), s.Put("", 1), get("zz"), get("")}
	for _, e := range bads {
		if e == nil || fmt.Sprint(s.HotKeys(), s.ColdKeys()) != before {
			t.Fatalf("拒绝留痕: %v", e)
		}
	}
	if v, _ := s.Value("a"); v != 1 {
		t.Fatal("换出丢值")
	}
	if _, e := s.Get("a"); e != nil {
		t.Fatalf("拒绝后不可用: %v", e)
	}
}
func TestConcurrentGet(t *testing.T) {
	const n = 64
	s, _ := api.New(n, n)
	for i := 0; i < n; i++ {
		_ = s.Put(fmt.Sprintf("k%d", i), int64(i))
	}
	parallel(n, func(i int) {
		for j := 0; j < 50; j++ {
			if v, e := s.Get(fmt.Sprintf("k%d", (i+j)%n)); e != nil || v != int64((i+j)%n) {
				t.Errorf("get: %d %v", v, e)
				return
			}
		}
	})
	checkTiers(t, s, n, n, n)
	if e := api.SelfCheck(); e != nil {
		t.Fatal(e)
	}
}
func TestConcurrentValue(t *testing.T) {
	s, _ := api.New(2, 4)
	_ = s.Put("x", 42)
	parallel(32, func(int) {
		if v, ok := s.Value("x"); !ok || v != 42 {
			t.Errorf("Value = %d,%v", v, ok)
		}
	})
}
