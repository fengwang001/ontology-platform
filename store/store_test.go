package store_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/store"
)

func TestReplayConsistency(t *testing.T) {
	cases := []struct {
		k    int
		keys []string
		puts int
	}{
		{1, []string{"a"}, 20}, {3, []string{"a", "b"}, 50}, {5, []string{"x", "y", "z"}, 100},
	}
	for _, c := range cases {
		s, err := store.New(c.k)
		if err != nil {
			t.Fatal(err)
		}
		last, maxV := map[string]string{}, map[string]int64{}
		for i := 1; i <= c.puts; i++ {
			key := c.keys[i%len(c.keys)]
			val := fmt.Sprintf("%s-%d", key, i)
			v, perr := s.Put(key, val)
			if perr != nil {
				t.Fatal(perr)
			}
			if v != maxV[key]+1 { // per-key versions start at 1, never skip
				t.Fatalf("k=%d key=%s: version %d after max %d", c.k, key, v, maxV[key])
			}
			maxV[key], last[key] = v, val
			if n := s.Len(key); n > c.k {
				t.Fatalf("key=%s retained %d > K %d", key, n, c.k)
			}
			if got, ok, _ := s.Get(key); !ok || got != last[key] { // Get == replay last
				t.Fatalf("key=%s Get=%q,%v want replay last %q", key, got, ok, last[key])
			}
		}
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	for _, k := range []int{0, -1} {
		if _, err := store.New(k); !errors.Is(err, store.ErrBadLimit) {
			t.Fatalf("New(%d) err=%v want ErrBadLimit", k, err)
		}
	}
	s, _ := store.New(3)
	v1, _ := s.Put("k", "first")
	badOps := []struct {
		name string
		run  func() error
	}{
		{"Put empty", func() error { _, e := s.Put("", "x"); return e }},
		{"Get empty", func() error { _, _, e := s.Get(""); return e }},
		{"GetAt empty", func() error { _, _, e := s.GetAt("", 1); return e }},
		{"GetAt zero", func() error { _, _, e := s.GetAt("k", 0); return e }},
		{"GetAt negative", func() error { _, _, e := s.GetAt("k", -7); return e }},
	}
	for _, op := range badOps {
		err := op.run()
		if !errors.Is(err, store.ErrEmptyKey) && !errors.Is(err, store.ErrInvalidVersion) {
			t.Fatalf("%s: err=%v is not a classifiable sentinel", op.name, err)
		}
		if s.Len("k") != 1 {
			t.Fatalf("%s: Len changed to %d", op.name, s.Len("k"))
		}
		if got, _, _ := s.GetAt("k", v1); got != "first" {
			t.Fatalf("%s: state changed, v1=%q", op.name, got)
		}
	}
	if v2, _ := s.Put("k", "second"); v2 != v1+1 { // still usable, no reuse
		t.Fatalf("version after rejections = %d, want %d", v2, v1+1)
	}
}

func TestAPISentinelsDistinct(t *testing.T) {
	a, err := api.New(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.New(0); !errors.Is(err, store.ErrBadLimit) {
		t.Fatalf("api.New(0) err=%v want ErrBadLimit", err)
	}
	_, e1 := a.Put("", "x")
	_, _, e2 := a.GetAt("", 1)
	_, _, e3 := a.GetAt("k", 0)
	for _, p := range [][2]error{{e1, store.ErrEmptyKey}, {e2, store.ErrEmptyKey}, {e3, store.ErrInvalidVersion}} {
		if !errors.Is(p[0], p[1]) {
			t.Fatalf("err=%v want %v", p[0], p[1])
		}
	}
	if errors.Is(e1, e3) || errors.Is(e3, e1) || e1 == e3 { // mutually distinct
		t.Fatalf("sentinels not distinct: %v vs %v", e1, e3)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentReaders(t *testing.T) {
	a, _ := api.New(3)
	for i := 0; i < 10; i++ {
		a.Put("hot", fmt.Sprintf("v%d", i))
	}
	const n = 64
	type view struct {
		val string
		ok  bool
		nn  int
	}
	views := make([]view, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			val, ok := a.Get("hot")
			views[i] = view{val, ok, a.Len("hot")}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if views[i] != views[0] {
			t.Fatalf("reader %d got %+v, want %+v", i, views[i], views[0])
		}
	}
}
