package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

var canonical = []api.Event{{"a", 0}, {"a", 0}, {"a", 0}, {"b", 1}, {"a", 0}, {"a", 0}, {"c", 1}, {"a", 0}}
var scenarios = []struct {
	name string
	S, T int
	evs  []api.Event
}{
	{"canonical", 2, 3, canonical},
	{"T1", 2, 1, []api.Event{{"a", 0}, {"a", 0}, {"b", 1}, {"b", 1}}},
	{"multihot", 3, 2, []api.Event{{"a", 0}, {"b", 0}, {"a", 0}, {"c", 2}, {"b", 0}, {"a", 0}}},
}

// naive is an independent brute-force simulation of the routing rules.
func naive(S, T int, evs []api.Event) []int64 {
	cnt := make([]int64, S)
	hot, ded, kc := map[string]bool{}, map[string]int{}, map[string]int64{}
	for _, e := range evs {
		if hot[e.Key] {
			cnt[ded[e.Key]]++
			continue
		}
		cnt[e.Base]++
		if kc[e.Key]++; kc[e.Key] >= int64(T) {
			ded[e.Key], hot[e.Key] = S+len(ded), true
			cnt = append(cnt, 0)
		}
	}
	return cnt
}
func feed(t *testing.T, S, T int, evs []api.Event) *api.System {
	sys, err := api.New(S, T)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Feed(evs); err != nil {
		t.Fatal(err)
	}
	return sys
}
func TestConservation(t *testing.T) {
	for _, sc := range scenarios {
		sys := feed(t, sc.S, sc.T, sc.evs)
		var n int64
		for _, c := range sys.Counts() {
			n += c
		}
		if n != int64(len(sc.evs)) {
			t.Fatalf("%s: total %d != fed %d", sc.name, n, len(sc.evs))
		}
	}
}
func TestCountsMatchesNaiveReference(t *testing.T) {
	for _, sc := range scenarios {
		got := feed(t, sc.S, sc.T, sc.evs).Counts()
		if want := naive(sc.S, sc.T, sc.evs); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v want %v", sc.name, got, want)
		}
	}
}
func TestInvalidConstruction(t *testing.T) {
	for _, c := range []struct {
		S, T int
		want error
	}{
		{0, 1, api.ErrInvalidShards}, {-3, 1, api.ErrInvalidShards},
		{1, 0, api.ErrInvalidThresh}, {2, -7, api.ErrInvalidThresh},
	} {
		if sys, err := api.New(c.S, c.T); sys != nil || !errors.Is(err, c.want) {
			t.Fatalf("New(%d,%d)=(%v,%v)", c.S, c.T, sys, err)
		}
	}
	seen := map[error]bool{}
	for _, e := range []error{api.ErrInvalidShards, api.ErrInvalidThresh, api.ErrBaseOutOfRange, api.ErrEmptyKey} {
		if seen[e] {
			t.Fatal("sentinel errors must be pairwise distinct")
		}
		seen[e] = true
	}
}
func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	sys, _ := api.New(2, 3)
	_ = sys.Feed(canonical)
	snap := func() string {
		d, ok := sys.Dedicated("a")
		return fmt.Sprintf("%v|%v|%d,%v", sys.Counts(), sys.IsHot("a"), d, ok)
	}
	for _, c := range []struct {
		want error
		evs  []api.Event
	}{
		{api.ErrBaseOutOfRange, []api.Event{{"z", -1}}},
		{api.ErrBaseOutOfRange, []api.Event{{"z", 2}}},
		{api.ErrEmptyKey, []api.Event{{"", 0}}},
		{api.ErrEmptyKey, []api.Event{{"z", 0}, {"", 0}}},
	} {
		before := snap()
		if err := sys.Feed(c.evs); !errors.Is(err, c.want) {
			t.Fatal(err)
		}
		if after := snap(); after != before {
			t.Fatalf("state changed %s -> %s", before, after)
		}
	}
	if err := sys.Feed([]api.Event{{"d", 1}, {"d", 1}, {"d", 1}}); err != nil { // d reaches T -> shard 3
		t.Fatal(err)
	}
	if sh, ok := sys.Dedicated("d"); !ok || sh != 3 {
		t.Fatalf("d dedicated=(%d,%v) want 3", sh, ok)
	}
}
func TestConcurrentReaders(t *testing.T) {
	sys, _ := api.New(2, 3)
	_ = sys.Feed(canonical)
	want := sys.Counts()
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := ""
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got := sys.Counts()
				_, _ = sys.Dedicated("a")
				_ = sys.IsHot("a")
				if err := sys.SelfCheck(); err != nil || !reflect.DeepEqual(got, want) {
					mu.Lock()
					bad = fmt.Sprintf("got %v want %v (err=%v)", got, want, err)
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	if bad != "" {
		t.Fatal(bad)
	}
}
