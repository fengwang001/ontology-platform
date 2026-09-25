package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// TestReplayConsistency nails invariant 1: Get equals naive replay per key.
func TestReplayConsistency(t *testing.T) {
	for _, k := range []int{1, 3, 5} {
		a, err := api.New(k)
		if err != nil {
			t.Fatal(err)
		}
		replay := map[string]string{}
		for i := 0; i < 200; i++ {
			key, val := fmt.Sprintf("key-%d", i%7), fmt.Sprintf("v%d", i)
			if _, err := a.Put(key, val); err != nil {
				t.Fatal(err)
			}
			replay[key] = val
		}
		for key, want := range replay {
			got, ok := a.Get(key)
			if !ok || got != want {
				t.Fatalf("k=%d Get(%q)=%q,%v want %q", k, key, got, ok, want)
			}
			if n := a.Len(key); n > k {
				t.Fatalf("k=%d Len(%q)=%d exceeds K", k, key, n)
			}
		}
	}
}

// TestRetentionBounds nails invariant 2: retained = newest K, contiguous, never reused.
func TestRetentionBounds(t *testing.T) {
	for _, c := range []struct{ k, puts int }{{1, 9}, {3, 6}, {4, 50}} {
		a, _ := api.New(c.k)
		for i := 1; i <= c.puts; i++ {
			if v, err := a.Put("k", fmt.Sprintf("v%d", i)); err != nil || v != int64(i) {
				t.Fatalf("put %d: v=%d err=%v", i, v, err)
			}
		}
		wantLen := min(c.puts, c.k)
		if a.Len("k") != wantLen {
			t.Fatalf("k=%d puts=%d: Len=%d want %d", c.k, c.puts, a.Len("k"), wantLen)
		}
		for v := int64(c.puts - wantLen + 1); v <= int64(c.puts); v++ {
			if got, ok, err := a.GetAt("k", v); err != nil || !ok || got != fmt.Sprintf("v%d", v) {
				t.Fatalf("GetAt(%d)=%q,%v,%v want retained", v, got, ok, err)
			}
		}
	}
}

// TestVisibilityBoundary nails invariant 3: GetAt hits iff v is in the retained window.
func TestVisibilityBoundary(t *testing.T) {
	a, _ := api.New(3)
	for _, v := range []string{"a", "b", "c", "d", "e", "f"} {
		if _, err := a.Put("k", v); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		v    int64
		want string
	}{
		{1, ""}, {2, ""}, {3, ""}, // cleaned
		{4, "d"}, {5, "e"}, {6, "f"}, // retained window [4,6]
		{7, ""}, {100, ""}, // beyond max version
	}
	for _, c := range cases {
		if got, ok, err := a.GetAt("k", c.v); err != nil || ok != (c.want != "") || got != c.want {
			t.Fatalf("GetAt(%d)=%q,%v,%v want %q", c.v, got, ok, err, c.want)
		}
	}
}

// TestRejectedOpsNoStateChange nails invariant 4: rejections fail with distinct sentinels and mutate nothing.
func TestRejectedOpsNoStateChange(t *testing.T) {
	for _, k := range []int{0, -2} {
		if _, err := api.New(k); !errors.Is(err, api.ErrInvalidK) {
			t.Fatalf("New(%d) must fail with ErrInvalidK", k)
		}
	}
	a, _ := api.New(3)
	for i := 0; i < 5; i++ {
		if _, err := a.Put("k", fmt.Sprintf("v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	before := a.Len("k")
	if _, err := a.Put("", "x"); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatal("empty-key Put must fail with ErrEmptyKey")
	}
	for _, v := range []int64{0, -3} {
		if _, _, err := a.GetAt("k", v); !errors.Is(err, api.ErrInvalidVersion) {
			t.Fatalf("GetAt v=%d must fail with ErrInvalidVersion", v)
		}
	}
	if _, _, err := a.GetAt("", 1); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatal("empty-key GetAt must fail with ErrEmptyKey")
	}
	if a.Len("k") != before {
		t.Fatal("rejections mutated retained history")
	}
	if v, err := a.Put("k", "next"); err != nil || v != 6 {
		t.Fatalf("after rejections next version=%d err=%v, want 6", v, err)
	}
	if got, ok := a.Get("k"); !ok || got != "next" {
		t.Fatal("instance must keep working after rejections")
	}
}

// TestConcurrentReaders: N goroutines read one fed instance; all results identical.
func TestConcurrentReaders(t *testing.T) {
	a, _ := api.New(5)
	for i := 0; i < 50; i++ {
		if _, err := a.Put("k", fmt.Sprintf("v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	wantGet, _ := a.Get("k")
	wantLen := a.Len("k")
	const n = 64
	type res struct {
		g, at string
		l     int
		ok    bool
	}
	results := make([]res, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			g, _ := a.Get("k")
			at, ok, _ := a.GetAt("k", 50)
			results[i] = res{g, at, a.Len("k"), ok}
		})
	}
	wg.Wait()
	for i, r := range results {
		if r.g != wantGet || r.l != wantLen || r.at != "v49" || !r.ok {
			t.Fatalf("goroutine %d saw %+v, want get=%q len=%d at=v49", i, r, wantGet, wantLen)
		}
	}
}
