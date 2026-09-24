package api_test

import (
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"

	"ontology/api"
	"ontology/fileref"
	"ontology/snapchain"
)

func sortedKeys(m map[string]struct{}) []string {
	o := []string{}
	for k := range m {
		o = append(o, k)
	}
	sort.Strings(o)
	return o
}
func unionMap(ss []snapchain.Snapshot) map[string]struct{} {
	m := map[string]struct{}{}
	for _, s := range ss {
		for _, f := range s.Files {
			m[f] = struct{}{}
		}
	}
	return m
}

// coherent pins invariants 2+3: stored files == union of snapshot files.
func coherent(t *testing.T, tab *api.Table) {
	snaps, files := tab.State()
	if !reflect.DeepEqual(files, sortedKeys(unionMap(snaps))) {
		t.Fatalf("incoherent files=%v", files)
	}
}

// TestNaiveReferenceRandom pins invariant 1 vs a naive model, plus 2+3.
func TestNaiveReferenceRandom(t *testing.T) {
	rng, tab := rand.New(rand.NewSource(7)), api.New()
	var ts, fresh int64
	for it := 0; it < 200; it++ {
		before := tab.Snapshots()
		if len(before) > 0 && rng.Intn(2) == 0 {
			n, T := rng.Intn(len(before)+2), rng.Int63n(ts+2)
			del, _ := tab.Expire(n, T)
			kept := []snapchain.Snapshot{}
			for i, s := range before {
				if (n > 0 && i >= len(before)-n) || s.TS > T || i == len(before)-1 {
					kept = append(kept, s)
				}
			}
			if !reflect.DeepEqual(tab.Snapshots(), kept) {
				t.Fatal("retained chain differs from naive model")
			}
			gone := unionMap(before)
			for f := range unionMap(kept) {
				delete(gone, f)
			}
			if !reflect.DeepEqual(del, sortedKeys(gone)) {
				t.Fatalf("iter %d deleted %v want %v", it, del, sortedKeys(gone))
			}
		} else {
			ts += 1 + int64(rng.Intn(3))
			fresh++
			var rem []string
			if len(before) > 0 && rng.Intn(2) == 0 {
				if fs := before[len(before)-1].Files; len(fs) > 0 {
					rem = []string{fs[rng.Intn(len(fs))]}
				}
			}
			tab.Commit(ts, []string{"f" + strconv.FormatInt(fresh, 10)}, rem)
		}
		coherent(t, tab)
	}
}
func TestRetainedReadable(t *testing.T) {
	tab := api.New()
	tab.Commit(1, []string{"a", "b"}, nil)
	tab.Commit(2, []string{"c"}, []string{"a"})
	tab.Expire(2, 0)
	if !reflect.DeepEqual(tab.Files(), []string{"a", "b", "c"}) {
		t.Fatal("retained S1 file a lost")
	}
}
func TestNoLeakAfterExpire(t *testing.T) {
	tab := api.New()
	if del, err := tab.Expire(0, 0); err != nil || len(del) != 0 {
		t.Fatal("empty expire must be a no-op")
	}
	tab.Commit(1, []string{"a", "b"}, nil)
	tab.Commit(2, []string{"c"}, []string{"a"})
	if del, _ := tab.Expire(0, 1); !reflect.DeepEqual(del, []string{"a"}) {
		t.Fatalf("deleted %v want [a]", del)
	}
	coherent(t, tab)
}

// TestRejectedOpsLeaveNoTrace pins invariant 4.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	tab := api.New()
	tab.Commit(10, []string{"f1", "f2"}, nil)
	_, e1 := tab.Expire(-1, 0)
	_, e2 := tab.Commit(10, []string{"x"}, nil)
	_, e3 := tab.Commit(11, []string{"f1"}, nil)
	_, e4 := tab.Commit(11, nil, []string{"zz"})
	if e1 != fileref.ErrNegativeN || e2 != snapchain.ErrTSNotIncreasing || e3 != fileref.ErrNameConflict || e4 != snapchain.ErrFileNotInCurrent {
		t.Fatalf("errors %v %v %v %v", e1, e2, e3, e4)
	}
	if len(tab.Snapshots()) != 1 || !reflect.DeepEqual(tab.Files(), []string{"f1", "f2"}) {
		t.Fatal("rejected operation changed state")
	}
	if _, err := tab.Commit(11, []string{"g"}, []string{"f1"}); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReadConsistency: coherent reads vs one writer, no sleep.
func TestConcurrentReadConsistency(t *testing.T) {
	tab, start := api.New(), make(chan struct{})
	var wg sync.WaitGroup
	spawn := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; f() }()
	}
	spawn(func() {
		for i := 1; i <= 200; i++ {
			tab.Commit(int64(i), []string{"p" + strconv.Itoa(i)}, nil)
			if i%7 == 0 {
				tab.Expire(i%3, int64(i)/2)
			}
		}
	})
	for r := 0; r < 4; r++ {
		spawn(func() {
			for i := 0; i < 2000; i++ {
				coherent(t, tab)
			}
		})
	}
	close(start)
	wg.Wait()
}
