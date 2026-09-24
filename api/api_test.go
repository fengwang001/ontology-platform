package api

import (
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		n, k int
		feed []Change
		want error
	}{
		{"n zero", 0, 0, nil, ErrInvalidN},
		{"n negative", -2, 1, nil, ErrInvalidN},
		{"k zero", 5, 0, nil, ErrInvalidK},
		{"k above n", 5, 6, nil, ErrInvalidK},
		{"empty key", 5, 2, []Change{{Key: "a", Score: 1}, {Key: "", Score: 2}}, ErrEmptyKey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, err := New(c.n, c.k)
			if c.feed == nil {
				if !errors.Is(err, c.want) {
					t.Fatalf("New: got %v want %v", err, c.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: unexpected %v", err)
			}
			if err := e.Feed(c.feed); !errors.Is(err, c.want) {
				t.Fatalf("Feed: got %v want %v", err, c.want)
			}
		})
	}
	if ErrInvalidN == ErrInvalidK || ErrInvalidK == ErrEmptyKey || ErrInvalidN == ErrEmptyKey {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}

func TestRejectedNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		bad   []Change
		after []Change
	}{
		{"empty first", []Change{{Key: "", Score: 100}, {Key: "z", Score: 100}},
			[]Change{{Key: "d", Score: 11}}},
		{"empty middle", []Change{{Key: "z", Score: 100}, {Key: "", Score: 100}, {Key: "y", Score: 100}},
			[]Change{{Key: "d", Score: 11}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := New(3, 2)
			if err := e.Feed([]Change{{Key: "a", Score: 10}, {Key: "b", Score: 9}, {Key: "c", Score: 8}}); err != nil {
				t.Fatal(err)
			}
			snapshot := e.TopK()
			if err := e.Feed(c.bad); !errors.Is(err, ErrEmptyKey) {
				t.Fatalf("reject: got %v", err)
			}
			if got := e.TopK(); !reflect.DeepEqual(got, snapshot) {
				t.Fatalf("state changed after reject: got %v want %v", got, snapshot)
			}
			if err := e.Feed(c.after); err != nil {
				t.Fatalf("engine unusable after reject: %v", err)
			}
			// a+10 evicted: window is b9, c8, d11 -> d then b.
			if got, want := e.TopK(), []Entry{{Key: "d", Sum: 11}, {Key: "b", Sum: 9}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("post-reject feed: got %v want %v", got, want)
			}
		})
	}
}

func TestSelfCheck(t *testing.T) {
	e, err := New(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Feed([]Change{{Key: "a", Score: 6}, {Key: "a", Score: 4}, {Key: "b", Score: 9},
		{Key: "c", Score: 8}, {Key: "d", Score: 7}, {Key: "e", Score: 1}, {Key: "f", Score: 8}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if got, want := e.TopK(), []Entry{{Key: "b", Sum: 9}, {Key: "c", Sum: 8}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestConcurrentAPI(t *testing.T) {
	e, _ := New(1000, 10)
	var batch []Change
	for i := 0; i < 1000; i++ {
		batch = append(batch, Change{Key: "k" + strconv.Itoa(i%91), Score: int64(i%17 - 8)})
	}
	if err := e.Feed(batch); err != nil {
		t.Fatal(err)
	}
	want := e.TopK()
	const readers = 24
	var wg sync.WaitGroup
	results := make([][]Entry, readers)
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				results[idx] = e.TopK()
				if err := e.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	for g, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reader %d: got %v want %v", g, got, want)
		}
	}
}
