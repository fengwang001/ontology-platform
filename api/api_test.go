package api_test

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

type nr struct {
	dep int64
	adm bool
}
type tseq struct {
	cap, iv int64
	ts      []int64
}

// genSeqs builds monotonic random sequences in a loop (ties and bursts occur).
func genSeqs() []tseq {
	r := rand.New(rand.NewSource(7))
	ss := make([]tseq, 20)
	for i := range ss {
		iv, t := int64(1+r.Intn(6)), int64(0)
		ts := make([]int64, 300)
		for j := range ts {
			t += int64(r.Intn(int(iv) + 3))
			ts[j] = t
		}
		ss[i] = tseq{int64(1 + r.Intn(8)), iv, ts}
	}
	return ss
}

// naive is the simple reference: pop the head while dep <= t, reject at cap.
func naive(c, iv int64, ts []int64) []nr {
	var dep []int64
	out := make([]nr, len(ts))
	for i, t := range ts {
		for len(dep) > 0 && dep[0] <= t {
			dep = dep[1:]
		}
		if int64(len(dep)) == c {
			continue
		}
		d := t + iv
		if len(dep) > 0 {
			d = dep[len(dep)-1] + iv
		}
		dep = append(dep, d)
		out[i] = nr{d, true}
	}
	return out
}

// runProp replays every sequence; mode selects the invariant to pin.
func runProp(t *testing.T, mode int) {
	for si, s := range genSeqs() {
		l, _ := api.New(s.cap, s.iv)
		want := naive(s.cap, s.iv, s.ts)
		prev, have := int64(0), false
		for i, at := range s.ts {
			dep, adm, serr := l.Submit(at)
			if mode == 0 && (serr != nil || dep != want[i].dep || adm != want[i].adm) {
				t.Fatalf("naive seq %d step %d t=%d: got (%d,%v,%v) want (%d,%v)",
					si, i, at, dep, adm, serr, want[i].dep, want[i].adm)
			}
			if mode == 1 && adm && have && dep < prev+s.iv {
				t.Fatalf("smooth seq %d step %d: %d->%d closer than %d", si, i, prev, dep, s.iv)
			}
			if mode == 2 && l.InSystem() > int(s.cap) {
				t.Fatalf("capacity seq %d step %d: %d > cap %d", si, i, l.InSystem(), s.cap)
			}
			if adm {
				prev, have = dep, true
			}
		}
	}
}
func TestNaiveReference(t *testing.T) { runProp(t, 0) }
func TestSmoothness(t *testing.T)     { runProp(t, 1) }
func TestCapacityBound(t *testing.T)  { runProp(t, 2) }

// TestRejectionLeavesStateUnchanged pins invariant 4: three distinct rejection kinds change no state.
func TestRejectionLeavesStateUnchanged(t *testing.T) {
	for i, c := range [][2]int64{{0, 5}, {3, 0}, {-1, 5}, {3, -2}} {
		if _, err := api.New(c[0], c[1]); !errors.Is(err, api.ErrInvalidConfig) {
			t.Fatalf("config case %d: %v", i, err)
		}
	}
	l, _ := api.New(3, 5)
	if _, _, err := l.Submit(-1); !errors.Is(err, api.ErrNegativeTime) {
		t.Fatalf("negative t: %v", err)
	}
	l.Submit(0)
	l.Submit(10)
	if _, _, err := l.Submit(9); !errors.Is(err, api.ErrClockRewind) {
		t.Fatalf("rewind: %v", err)
	}
	if errors.Is(api.ErrNegativeTime, api.ErrClockRewind) ||
		errors.Is(api.ErrInvalidConfig, api.ErrNegativeTime) {
		t.Fatal("sentinel errors are not mutually distinct")
	}
	if _, _, err := l.Submit(9); !errors.Is(err, api.ErrClockRewind) { // lastT stayed 10
		t.Fatal("lastT changed by a rejected rewind")
	}
	if dep, ok, err := l.Submit(10); err != nil || !ok || dep != 20 {
		t.Fatalf("queue altered by rejection: (%d,%v,%v) want 20", dep, ok, err)
	}
}

// TestConcurrentSubmit: same timestamp from N goroutines, no sleeps.
func TestConcurrentSubmit(t *testing.T) {
	for _, iv := range []int64{1, 5, 17} {
		l, _ := api.New(8, iv)
		var wg sync.WaitGroup
		var mu sync.Mutex
		deps := make([]int64, 0, 8)
		for range 256 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if d, ok, _ := l.Submit(100); ok {
					mu.Lock()
					deps = append(deps, d)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if len(deps) != 8 || l.InSystem() != 8 {
			t.Fatalf("iv=%d admitted=%d in-system=%d, want 8", iv, len(deps), l.InSystem())
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i] < deps[j] })
		for i := 1; i < len(deps); i++ {
			if deps[i] <= deps[i-1] {
				t.Fatalf("iv=%d departures not strictly increasing: %v", iv, deps)
			}
		}
	}
}
func TestSelfCheck(t *testing.T) {
	l, _ := api.New(3, 5)
	if err := l.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
