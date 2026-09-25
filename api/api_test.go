package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func TestFacadeAndSentinels(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrInvalidThreshold) {
		t.Fatalf("New(0): %v", err)
	}
	s, err := api.New(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("a", "1"); err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Read("a"); !ok || v != "1" {
		t.Fatalf("Read a = (%q,%v)", v, ok)
	}
	if err := s.Set("", "x"); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("Set empty key: %v", err)
	}
	if len(s.BaseKeys()) != 0 || s.DeltaLen() != 1 {
		t.Fatal("rejected empty-key Set changed state")
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestSelfCheckSevenStep(t *testing.T) {
	// Independently assert the documented T=4 end state via the facade.
	s, _ := api.New(4)
	ops := []struct {
		k, v string
		del  bool
	}{
		{"a", "1", false}, {"b", "2", false}, {"c", "3", false}, {"c", "", true},
		{"b", "9", false}, {"c", "7", false}, {"c", "8", false},
	}
	want := []int{1, 2, 3, 0, 1, 2, 3}
	for i, o := range ops {
		var err error
		if o.del {
			err = s.Del(o.k)
		} else {
			err = s.Set(o.k, o.v)
		}
		if err != nil || s.DeltaLen() != want[i] {
			t.Fatalf("step %d: err=%v deltaLen=%d want %d", i+1, err, s.DeltaLen(), want[i])
		}
	}
	if !reflect.DeepEqual(s.BaseKeys(), []string{"a", "b"}) {
		t.Fatalf("base after step 4/7 = %v", s.BaseKeys())
	}
	cases := map[string]struct {
		v  string
		ok bool
	}{
		"a": {"1", true}, "b": {"9", true}, "c": {"8", true}, "gone": {"", false},
	}
	_ = s.Del("gone") // present? no: ensure an absent key reads absent
	for k, w := range cases {
		if k == "gone" {
			if _, ok := s.Read(k); ok {
				t.Fatal("absent key read present")
			}
			continue
		}
		if gv, ok := s.Read(k); !ok || gv != w.v {
			t.Fatalf("Read %s = (%q,%v), want (%q,%v)", k, gv, ok, w.v, w.ok)
		}
	}
}

func TestConcurrentReadersWriters(t *testing.T) {
	for _, N := range []int{4, 16, 64} {
		filled, _ := api.New(64)
		const K = 50
		for i := 0; i < K; i++ {
			if err := filled.Set(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		// N readers: every goroutine must observe the same key-by-key values.
		var wg sync.WaitGroup
		got := make([][]string, N)
		for g := 0; g < N; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < K; i++ {
					v, ok := filled.Read(fmt.Sprintf("k%d", i))
					if !ok {
						v = "<absent>"
					}
					got[g] = append(got[g], v)
				}
			}(g)
		}
		wg.Wait()
		for g := 1; g < N; g++ {
			if !reflect.DeepEqual(got[g], got[0]) {
				t.Fatalf("N=%d reader %d diverged", N, g)
			}
		}
		// N writers on pairwise-distinct keys: result equals serial execution.
		ws, _ := api.New(64)
		const perG = 20
		for g := 0; g < N; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					_ = ws.Set(fmt.Sprintf("g%d-k%d", g, j), fmt.Sprintf("%d-%d", g, j))
				}
			}(g)
		}
		wg.Wait()
		for g := 0; g < N; g++ {
			for j := 0; j < perG; j++ {
				want := fmt.Sprintf("%d-%d", g, j)
				if v, ok := ws.Read(fmt.Sprintf("g%d-k%d", g, j)); !ok || v != want {
					t.Fatalf("N=%d key g%d-k%d = (%q,%v), want %q", N, g, j, v, ok, want)
				}
			}
		}
	}
}
