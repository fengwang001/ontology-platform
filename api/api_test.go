package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

func TestNewRejectsBadParams(t *testing.T) {
	cases := []struct {
		name string
		m, k int
	}{
		{"m zero", 0, 3}, {"m negative", -1, 3}, {"k zero", 8, 0}, {"both negative", -1, -2},
	}
	for _, c := range cases {
		if _, err := api.New(c.m, c.k); !errors.Is(err, api.ErrInvalidParams) {
			t.Fatalf("%s: got %v, want ErrInvalidParams", c.name, err)
		}
	}
}

func TestAddQueryRemoveFlow(t *testing.T) {
	f, err := api.New(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []int64{3, 5, 7} {
		if err := f.Add(key); err != nil {
			t.Fatal(err)
		}
		if v, _ := f.Query(key); v < 1 {
			t.Fatalf("Query(%d)=%d right after Add", key, v)
		}
	}
	if v, _ := f.Query(5); v < 1 {
		t.Fatalf("Query(5)=%d, want >=1", v)
	}
	if err := f.Remove(5); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.Query(5); v != 0 {
		t.Fatalf("Query(5)=%d after Remove, want 0", v)
	}
	if !f.SelfCheck() {
		t.Fatal("SelfCheck failed through public API")
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	f, _ := api.New(8, 3)
	if err := f.Add(-7); !errors.Is(err, api.ErrInvalidKey) {
		t.Fatalf("Add(-7)=%v", err)
	}
	if _, err := f.Query(-1); !errors.Is(err, api.ErrInvalidKey) {
		t.Fatalf("Query(-1)=%v", err)
	}
	if err := f.Remove(123); !errors.Is(err, api.ErrNotPresent) {
		t.Fatalf("Remove(absent)=%v", err)
	}
	if api.ErrInvalidParams == api.ErrInvalidKey ||
		api.ErrInvalidKey == api.ErrNotPresent ||
		api.ErrInvalidParams == api.ErrNotPresent {
		t.Fatal("the three sentinel errors must be pairwise distinct")
	}
}

func TestRejectedOpsReusable(t *testing.T) {
	f, _ := api.New(8, 3)
	if err := f.Add(2); err != nil {
		t.Fatal(err)
	}
	baseline, _ := f.Query(2)
	for i := 0; i < 3; i++ { // rejected calls must not poison the filter
		if err := f.Remove(4); !errors.Is(err, api.ErrNotPresent) {
			t.Fatalf("Remove(4)=%v", err)
		}
		if err := f.Add(-1); !errors.Is(err, api.ErrInvalidKey) {
			t.Fatalf("Add(-1)=%v", err)
		}
	}
	if v, _ := f.Query(2); v != baseline || v < 1 {
		t.Fatalf("filter state changed after rejections: Query(2)=%d baseline=%d", v, baseline)
	}
}

func TestConcurrentQueriesConsistent(t *testing.T) {
	f, _ := api.New(1024, 6)
	const batch = 256
	for key := int64(0); key < batch; key++ {
		if err := f.Add(key); err != nil {
			t.Fatal(err)
		}
	}
	expected := make([]int64, batch)
	for key := range expected {
		expected[key], _ = f.Query(int64(key))
	}
	const n = 32
	var wg sync.WaitGroup
	rows := make([][]int64, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			if g%4 == 0 { // some goroutines only SelfCheck: Query vs SelfCheck concurrency
				for i := 0; i < 50; i++ {
					if !f.SelfCheck() {
						t.Error("concurrent SelfCheck returned false")
						return
					}
				}
				rows[g] = expected
				return
			}
			row := make([]int64, batch)
			for key := int64(0); key < batch; key++ {
				row[key], _ = f.Query(key)
			}
			rows[g] = row
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		if fmt.Sprint(rows[g]) != fmt.Sprint(expected) {
			t.Fatalf("goroutine %d saw per-key results different from the baseline", g)
		}
	}
}
