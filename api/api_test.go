package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// TestSentinelErrorsRejectClean pins I4: three distinct sentinels and rejected
// ops leave parallelism, values and buckets untouched; engine stays usable.
func TestSentinelErrorsRejectClean(t *testing.T) {
	newCases := []struct {
		mp, p int
		want  error
	}{
		{0, 1, api.ErrMaxPInvalid}, {10, 0, api.ErrPInvalid}, {10, 11, api.ErrPInvalid},
	}
	for _, c := range newCases {
		if _, e := api.New(c.mp, c.p); !errors.Is(e, c.want) {
			t.Fatalf("New(%d,%d)=%v want %v", c.mp, c.p, e, c.want)
		}
	}
	if api.ErrMaxPInvalid == api.ErrPInvalid || api.ErrPInvalid == api.ErrEmptyKey ||
		api.ErrMaxPInvalid == api.ErrEmptyKey {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	e, _ := api.New(10, 3)
	if err := e.Put("u1", 1); err != nil {
		t.Fatal(err)
	}
	r0 := e.Ranges()
	rejects := []struct {
		fn   func() error
		want error
	}{
		{func() error { return e.Put("", 9) }, api.ErrEmptyKey},
		{func() error { _, er := e.Rescale(0); return er }, api.ErrPInvalid},
		{func() error { _, er := e.Rescale(11); return er }, api.ErrPInvalid},
	}
	for _, c := range rejects {
		if err := c.fn(); !errors.Is(err, c.want) {
			t.Fatalf("reject err=%v want %v", err, c.want)
		}
		if !reflect.DeepEqual(e.Ranges(), r0) {
			t.Fatal("ranges changed after rejection")
		}
		if v, ok := e.Get("u1"); !ok || v != 1 {
			t.Fatal("values changed after rejection")
		}
	}
	if err := e.Put("u2", 2); err != nil {
		t.Fatal("engine unusable after rejection")
	}
	if _, err := e.Rescale(4); err != nil {
		t.Fatal("legal rescale rejected after prior failures")
	}
}

// TestConcurrentReaders pins the concurrency requirement: N goroutines reading
// a populated engine see field-identical ranges and (owner,value) per key.
// No sleeps; synchronization is via WaitGroup only.
func TestConcurrentReaders(t *testing.T) {
	e, _ := api.New(10, 4)
	keys := make([]string, 64)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
		if err := e.Put(keys[i], int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	const n = 32
	var wg sync.WaitGroup
	type view struct {
		ranges [][2]int
		owners []int
		vals   []int64
	}
	views := make([]view, n)
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			v := view{ranges: e.Ranges(), owners: make([]int, len(keys)), vals: make([]int64, len(keys))}
			for i, k := range keys {
				v.owners[i] = e.Owner(k)
				v.vals[i], _ = e.Get(k)
			}
			views[g] = v
		}(g)
	}
	wg.Wait()
	for g := 1; g < n; g++ {
		if !reflect.DeepEqual(views[g].ranges, views[0].ranges) ||
			!reflect.DeepEqual(views[g].owners, views[0].owners) ||
			!reflect.DeepEqual(views[g].vals, views[0].vals) {
			t.Fatalf("reader %d saw a different view", g)
		}
	}
}

// TestConcurrentReadWrite exercises concurrent readers with Put/Rescale under
// -race; the reader closes stop when done so the two mutators can finish.
func TestConcurrentReadWrite(t *testing.T) {
	e, _ := api.New(16, 4)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				_ = e.Put(fmt.Sprintf("c%d", i%50), int64(i))
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				_, _ = e.Rescale(1 + (i % 16))
			}
		}
	}()
	go func() {
		defer wg.Done()
		for j := 0; j < 2000; j++ {
			_ = e.Ranges()
			_, _ = e.Get(fmt.Sprintf("c%d", j%50))
			_ = e.Owner(fmt.Sprintf("c%d", j%50))
		}
		close(stop)
	}()
	wg.Wait()
	if err := e.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
