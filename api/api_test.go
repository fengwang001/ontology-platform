package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestErrorsDistinct: the four sentinels are mutually
// distinguishable via errors.Is, and each is triggerable.
func TestErrorsDistinct(t *testing.T) {
	all := []error{api.ErrBadParam, api.ErrOutOfRange, api.ErrNotFound, api.ErrTooMany}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %d unexpectedly matches %d", i, j)
			}
		}
	}
	v, err := api.New(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Put(0, 1); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { _, e := v.Put(0, 1e10); return e }, api.ErrBadParam},
		{func() error { _, e := v.Put(4, 1); return e }, api.ErrOutOfRange},
		{func() error { _, e := v.Del(2); return e }, api.ErrNotFound},
		{func() error { _, e := v.Prefix(2); return e }, api.ErrNotFound},
		{func() error { _, e := v.Put(2, 1); return e }, api.ErrTooMany},
	}
	for i, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("case %d: got %v, want %v", i, err, c.want)
		}
	}
}

// TestConcurrentReaders: many goroutines read one filled instance;
// every observed view must be field-by-field identical.
func TestConcurrentReaders(t *testing.T) {
	v, err := api.New(4096, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2048; i++ {
		if _, err := v.Put(int64(2*i), int64(i%11-5)); err != nil {
			t.Fatal(err)
		}
	}
	golden := v.View()
	const readers = 32
	views := make([]map[int64]int64, readers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				views[g] = v.View()
				if _, err := v.Prefix(int64(2 * (i % 2048))); err != nil {
					t.Errorf("Prefix: %v", err)
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g, got := range views {
		if !reflect.DeepEqual(got, golden) {
			t.Errorf("reader %d saw a different view", g)
		}
	}
}

// TestConcurrentReadWrite: readers, writers and SelfCheck run
// together; only the race detector can fail this test.
func TestConcurrentReadWrite(t *testing.T) {
	v, err := api.New(256, 256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Put(0, 1); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 200; i++ {
				_, _ = v.Put(int64((g*200+i)%256), int64(i-g))
				_, _ = v.Prefix(int64((g*200 + i) % 256))
				_ = v.View()
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 3; i++ {
			if err := api.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}
	}()
	close(start)
	wg.Wait()
}
