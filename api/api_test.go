package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/lim"
)

// naiveDec is the step-by-step reference: scan accepted ts in [t-window, t].
func naiveDec(limit, window int64, trace []int64) []bool {
	hist, dec := []int64{}, make([]bool, len(trace))
	for i, t := range trace {
		k := 0
		for _, ts := range hist {
			if ts >= t-window && ts <= t {
				k++
			}
		}
		if dec[i] = int64(k) < limit; dec[i] {
			hist = append(hist, t)
		}
	}
	return dec
}

func TestEightStepDecisions(t *testing.T) {
	ts := []int64{0, 2, 5, 7, 10, 11, 12, 20}
	wantA := []bool{true, true, true, false, false, true, false, true}
	wantSet := [][]int64{{0}, {0, 2}, {0, 2, 5}, {0, 2, 5}, {0, 2, 5}, {2, 5, 11}, {2, 5, 11}, {11, 20}}
	l, _ := New(3, 10)
	for i, at := range ts {
		if a, err := l.Allow(at); err != nil || a != wantA[i] || !reflect.DeepEqual(l.Snapshot(), wantSet[i]) {
			t.Fatalf("Allow(%d)=%v,%v %v; want %v %v", at, a, err, l.Snapshot(), wantA[i], wantSet[i])
		}
	}
}

func TestAllowMatchesNaiveReference(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for it := 0; it < 500; it++ {
		limit, window := int64(r.Intn(6)+1), int64(r.Intn(25)+1)
		trace := make([]int64, r.Intn(80)+1)
		cur := int64(0)
		for i := range trace { // loop-generated non-decreasing random stamps
			cur += int64(r.Intn(3))
			trace[i] = cur
		}
		dec := naiveDec(limit, window, trace)
		l, _ := New(limit, window)
		lastAcc := int64(0)
		for i, at := range trace {
			a, err := l.Allow(at)
			if err != nil || a != dec[i] || (a && at < lastAcc) {
				t.Fatalf("it=%d t=%d: %v,%v want %v", it, at, a, err, dec[i])
			}
			if a {
				lastAcc = at
			}
		}
	}
}

func TestClockMonotonicity(t *testing.T) {
	cases := []struct {
		calls []int64
		want  error
	}{{[]int64{-1}, lim.ErrNegativeTime}, {[]int64{5, 4}, lim.ErrClockRewind}, {[]int64{5, -1}, lim.ErrNegativeTime}}
	for _, c := range cases {
		l, _ := New(3, 10)
		var err error
		for _, ts := range c.calls {
			_, err = l.Allow(ts)
		}
		if !errors.Is(err, c.want) {
			t.Fatalf("%v: %v want %v", c.calls, err, c.want)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	for _, b := range [][2]int64{{0, 10}, {-1, 10}, {3, 0}, {0, 0}} {
		if l, err := New(b[0], b[1]); l != nil || !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%v)=%v,%v", b, l, err)
		}
	}
	errs := []error{ErrInvalidConfig, lim.ErrNegativeTime, lim.ErrClockRewind}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("%v,%v must be distinct", errs[i], errs[j])
			}
		}
	}
}

func TestRejectedCallsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		bad  int64
		want error
	}{{-1, lim.ErrNegativeTime}, {4, lim.ErrClockRewind}}
	for _, c := range cases {
		l, _ := New(3, 10)
		l.Allow(5)
		set, n := append([]int64(nil), l.Snapshot()...), l.Accepted()
		if _, err := l.Allow(c.bad); !errors.Is(err, c.want) {
			t.Fatalf("Allow(%d): %v", c.bad, err)
		}
		if l.Accepted() != n || !reflect.DeepEqual(l.Snapshot(), set) {
			t.Fatal("rejected call changed state")
		}
		if a, err := l.Allow(5); err != nil || !a { // equal t legal; still usable
			t.Fatalf("unusable after rejection: %v,%v", a, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	l, _ := New(3, 10)
	if !l.SelfCheck() || !(*Limiter)(nil).SelfCheck() {
		t.Fatal("SelfCheck = false")
	}
}

// TestConcurrentSameTimestamp: channel-gated goroutines (no sleeps), same-t admits <= limit and equal Accepted.
func TestConcurrentSameTimestamp(t *testing.T) {
	for _, c := range []struct{ n, limit int64 }{{64, 1}, {128, 7}, {500, 3}, {1000, 100}} {
		l, _ := New(c.limit, 10)
		start, wg, admitted := make(chan struct{}), sync.WaitGroup{}, int64(0)
		for i := int64(0); i < c.n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if a, _ := l.Allow(100); a {
					atomic.AddInt64(&admitted, 1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if admitted > c.limit || int(admitted) != l.Accepted() {
			t.Fatalf("admitted=%d Accepted=%d limit=%d", admitted, l.Accepted(), c.limit)
		}
	}
}
