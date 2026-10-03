package ejector

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveSim is a deliberately plain, step-by-step simulation of the rules,
// written directly from the specification. The randomized test replays
// identical operation sequences against both Ejector and naiveSim and
// requires identical results and identical final states.
type naiveSim struct {
	n      int
	k      int64
	b      int64
	capD   int64
	p      int64
	wn     int
	q      int64
	c      []int64
	e      []int64
	u      []int64
	win    [][]bool
	maxNow int64
}

func newNaive(n int, k, b, capD int64, p, wn, q int) *naiveSim {
	return &naiveSim{
		n: n, k: k, b: b, capD: capD,
		p: int64(p), wn: wn, q: int64(q),
		c:   make([]int64, n),
		e:   make([]int64, n),
		u:   make([]int64, n),
		win: make([][]bool, n),
	}
}

func (s *naiveSim) check(host int, now int64) error {
	if host < 0 || host >= s.n {
		return ErrHostOutOfRange
	}
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	return nil
}

// report applies the Report rules step by step and returns the result plus
// a human-readable justification printed into the test log.
func (s *naiveSim) report(host int, ok bool, now int64) (Result, string) {
	s.maxNow = now
	if s.u[host] > now {
		return Ignored, fmt.Sprintf("u=%d > now=%d, host still ejected, report ignored", s.u[host], now)
	}
	if ok {
		s.c[host] = 0
		if s.e[host] > 0 {
			s.e[host]--
		}
		s.win[host] = append(s.win[host], true)
		if len(s.win[host]) > s.wn {
			s.win[host] = s.win[host][1:]
		}
		return Recorded, fmt.Sprintf("success: c=0, e->%d, win=%v", s.e[host], s.win[host])
	}
	s.c[host]++
	s.win[host] = append(s.win[host], false)
	if len(s.win[host]) > s.wn {
		s.win[host] = s.win[host][1:]
	}
	var f int64
	for _, okv := range s.win[host] {
		if !okv {
			f++
		}
	}
	condC := s.c[host] >= s.k
	condW := len(s.win[host]) == s.wn && f*100 >= s.q*int64(s.wn)
	if !condC && !condW {
		return Recorded, fmt.Sprintf("failure: c=%d<K=%d and window not triggering (len=%d,f=%d)",
			s.c[host], s.k, len(s.win[host]), f)
	}
	var ejectedNow int64
	for h := 0; h < s.n; h++ {
		if s.u[h] > now {
			ejectedNow++
		}
	}
	lhs := (ejectedNow + 1) * 100
	rhs := s.p * int64(s.n)
	if lhs > rhs {
		return Capped, fmt.Sprintf("triggered (c=%d>=K or f=%d in full window) but (E+1)*100=%d > P*N=%d, capped",
			s.c[host], f, lhs, rhs)
	}
	s.e[host]++
	d := s.b * s.e[host]
	if d > s.capD {
		d = s.capD
	}
	s.u[host] = now + d
	s.c[host] = 0
	s.win[host] = nil
	return Ejected, fmt.Sprintf("triggered (condC=%v,condW=%v), (E+1)*100=%d <= P*N=%d: ejected e=%d u=%d",
		condC, condW, lhs, rhs, s.e[host], s.u[host])
}

// batch applies the ReportBatch rules step by step: validate every event in
// input order, check the batch minimum against maxNow, stably sort by now,
// then apply each event; results stay aligned with the input order.
func (s *naiveSim) batch(events []Event) ([]Result, error, []string) {
	if len(events) == 0 {
		return []Result{}, nil, nil
	}
	for _, ev := range events {
		if ev.Host < 0 || ev.Host >= s.n {
			return nil, ErrHostOutOfRange, nil
		}
		if ev.Now < 0 || ev.Now > maxTime {
			return nil, ErrInvalidTime, nil
		}
	}
	minNow := events[0].Now
	for _, ev := range events[1:] {
		if ev.Now < minNow {
			minNow = ev.Now
		}
	}
	if minNow < s.maxNow {
		return nil, ErrClockRegression, nil
	}
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return events[order[a]].Now < events[order[b]].Now
	})
	results := make([]Result, len(events))
	reasons := make([]string, len(events))
	for _, idx := range order {
		r, why := s.report(events[idx].Host, events[idx].OK, events[idx].Now)
		results[idx] = r
		reasons[idx] = why
	}
	return results, nil, reasons
}

func (s *naiveSim) ejected(host int, now int64) (bool, error) {
	if err := s.check(host, now); err != nil {
		return false, err
	}
	return s.u[host] > now, nil
}

func (s *naiveSim) healthy(now int64) ([]int, error) {
	if now < 0 || now > maxTime {
		return nil, ErrInvalidTime
	}
	if now < s.maxNow {
		return nil, ErrClockRegression
	}
	out := []int{}
	for h := 0; h < s.n; h++ {
		if s.u[h] <= now {
			out = append(out, h)
		}
	}
	return out, nil
}

// errCategory maps an error to its rejection reason for comparison.
func errCategory(err error) error {
	switch {
	case errors.Is(err, ErrHostOutOfRange):
		return ErrHostOutOfRange
	case errors.Is(err, ErrInvalidTime):
		return ErrInvalidTime
	case errors.Is(err, ErrClockRegression):
		return ErrClockRegression
	default:
		return nil
	}
}

func winEqual(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// compareState requires the real Ejector and the naive simulation to hold
// exactly the same (c, e, u, win) for every host and the same maxNow.
func compareState(t *testing.T, g int, e *Ejector, s *naiveSim) {
	t.Helper()
	if e.maxNow != s.maxNow {
		t.Fatalf("group %d: maxNow=%d, naive=%d", g, e.maxNow, s.maxNow)
	}
	for h := 0; h < s.n; h++ {
		got := e.hosts[h]
		if got.c != s.c[h] || got.e != s.e[h] || got.u != s.u[h] || !winEqual(got.win, s.win[h]) {
			t.Fatalf("group %d host %d: real (c=%d,e=%d,u=%d,win=%v), naive (c=%d,e=%d,u=%d,win=%v)",
				g, h, got.c, got.e, got.u, got.win, s.c[h], s.e[h], s.u[h], s.win[h])
		}
	}
}

// TestRandomAgainstNaive replays 2000 random operation sequences (single
// reports, out-of-order batches, queries, invalid inputs) against both the
// real Ejector and the naive simulation, comparing every return value and
// the full final state. A second real Ejector replays the same sequence to
// confirm deterministic replay. Inputs, outputs and the decision basis are
// printed into the test log (visible with -v).
func TestRandomAgainstNaive(t *testing.T) {
	const groups = 2000
	rng := rand.New(rand.NewPCG(20261003, 1120))

	for g := 0; g < groups; g++ {
		n := 1 + rng.IntN(6)
		k := int64(1 + rng.IntN(5))
		b := int64(1 + rng.IntN(50))
		capD := b + int64(rng.IntN(100))
		p := rng.IntN(101)
		wn := 1 + rng.IntN(8)
		q := 1 + rng.IntN(100)

		real, err := NewEjector(n, k, b, capD, p, wn, q)
		if err != nil {
			t.Fatalf("group %d: NewEjector: %v", g, err)
		}
		replay, err := NewEjector(n, k, b, capD, p, wn, q)
		if err != nil {
			t.Fatalf("group %d: NewEjector(replay): %v", g, err)
		}
		naive := newNaive(n, k, b, capD, p, wn, q)

		t.Logf("group %d: config N=%d K=%d B=%d Cap=%d P=%d Wn=%d Q=%d", g, n, k, b, capD, p, wn, q)

		numOps := 5 + rng.IntN(36)
		for i := 0; i < numOps; i++ {
			maxNow := real.maxNow
			switch rng.IntN(10) {
			case 0, 1, 2, 3, 4, 5: // single report
				host := rng.IntN(n)
				now := maxNow + int64(rng.IntN(4))
				switch rng.IntN(20) {
				case 0:
					host = n + rng.IntN(2) // host out of range
				case 1:
					now = -1 - int64(rng.IntN(3)) // invalid time
				case 2:
					if maxNow > 0 {
						now = maxNow - 1 // clock regression
					}
				}
				ok := rng.IntN(2) == 0
				got, gErr := real.Report(host, ok, now)
				got2, gErr2 := replay.Report(host, ok, now)
				if got != got2 || errCategory(gErr) != errCategory(gErr2) {
					t.Fatalf("group %d op %d: replay mismatch %v,%v vs %v,%v", g, i, got, gErr, got2, gErr2)
				}
				if gErr != nil {
					nErr := naive.check(host, now)
					if errCategory(gErr) != errCategory(nErr) {
						t.Fatalf("group %d op %d: Report(%d,%v,%d) err=%v, naive err=%v",
							g, i, host, ok, now, gErr, nErr)
					}
					t.Logf("group %d op %d: Report(host=%d,ok=%v,now=%d) -> rejected: %v", g, i, host, ok, now, gErr)
				} else {
					want, why := naive.report(host, ok, now)
					if got != want {
						t.Fatalf("group %d op %d: Report(%d,%v,%d)=%v, naive=%v (%s)",
							g, i, host, ok, now, got, want, why)
					}
					t.Logf("group %d op %d: Report(host=%d,ok=%v,now=%d) -> %v (%s)", g, i, host, ok, now, got, why)
				}
			case 6, 7: // out-of-order batch
				size := 1 + rng.IntN(6)
				events := make([]Event, size)
				for j := range events {
					events[j] = Event{
						Host: rng.IntN(n),
						OK:   rng.IntN(2) == 0,
						Now:  maxNow + int64(rng.IntN(8)),
					}
					switch rng.IntN(30) {
					case 0:
						events[j].Host = -1 // host out of range
					case 1:
						events[j].Now = maxTime + 1 // invalid time
					case 2:
						if maxNow > 0 {
							events[j].Now = maxNow - 1 // clock regression
						}
					}
				}
				got, gErr := real.ReportBatch(events)
				got2, gErr2 := replay.ReportBatch(events)
				if errCategory(gErr) != errCategory(gErr2) || !reflect.DeepEqual(got, got2) {
					t.Fatalf("group %d op %d: batch replay mismatch", g, i)
				}
				want, nErr, reasons := naive.batch(events)
				if errCategory(gErr) != errCategory(nErr) {
					t.Fatalf("group %d op %d: batch err=%v, naive err=%v", g, i, gErr, nErr)
				}
				if gErr == nil && !reflect.DeepEqual(got, want) {
					t.Fatalf("group %d op %d: batch results %v, naive %v", g, i, got, want)
				}
				var b strings.Builder
				fmt.Fprintf(&b, "group %d op %d: ReportBatch(%v)", g, i, events)
				if gErr != nil {
					fmt.Fprintf(&b, " -> rejected: %v", gErr)
				} else {
					for j := range got {
						fmt.Fprintf(&b, "\n    input[%d] -> %v (%s)", j, got[j], reasons[j])
					}
				}
				t.Log(b.String())
			case 8: // Ejected query
				host := rng.IntN(n)
				now := maxNow + int64(rng.IntN(4))
				if rng.IntN(15) == 0 && maxNow > 0 {
					now = maxNow - 1
				}
				got, gErr := real.Ejected(host, now)
				want, nErr := naive.ejected(host, now)
				if got != want || errCategory(gErr) != errCategory(nErr) {
					t.Fatalf("group %d op %d: Ejected(%d,%d)=%v,%v, naive=%v,%v",
						g, i, host, now, got, gErr, want, nErr)
				}
				t.Logf("group %d op %d: Ejected(host=%d,now=%d) -> %v, err=%v", g, i, host, now, got, gErr)
			default: // Healthy query
				now := maxNow + int64(rng.IntN(4))
				got, gErr := real.Healthy(now)
				want, nErr := naive.healthy(now)
				if errCategory(gErr) != errCategory(nErr) {
					t.Fatalf("group %d op %d: Healthy(%d) err=%v, naive err=%v", g, i, now, gErr, nErr)
				}
				if gErr == nil && !reflect.DeepEqual(got, want) {
					t.Fatalf("group %d op %d: Healthy(%d)=%v, naive=%v", g, i, now, got, want)
				}
				t.Logf("group %d op %d: Healthy(now=%d) -> %v, err=%v", g, i, now, got, gErr)
			}
			compareState(t, g, real, naive)
		}
		compareState(t, g, replay, naive)
	}
}

// TestConcurrentAccess hammers one Ejector from many goroutines while a
// reader runs queries; with -race this proves the mutex serializes all
// access. Afterwards the structural invariants are verified.
func TestConcurrentAccess(t *testing.T) {
	const (
		n         = 8
		wn        = 4
		p         = 50
		writers   = 8
		perWriter = 200
	)
	e := mustNew(t, n, 2, 5, 50, p, wn, 50)

	var seq atomic.Int64
	var writersWG sync.WaitGroup
	var stop atomic.Bool
	for g := 0; g < writers; g++ {
		writersWG.Add(1)
		go func(g int) {
			defer writersWG.Done()
			for i := 0; i < perWriter; i++ {
				now := seq.Add(1) - 1
				// Unique increasing timestamps may still arrive at the lock
				// out of order; regressions are simply rejected.
				_, _ = e.Report(g%n, i%3 != 0, now)
			}
		}(g)
	}
	limit := int64(p) * int64(n) / 100
	// Reader: run queries and, while holding the lock, verify the ejection
	// cap at the current maxNow (a genuine, consistent moment).
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for i := 0; !stop.Load(); i++ {
			now := seq.Load()
			_, _ = e.Ejected(i%n, now)
			_, _ = e.Healthy(now)
			e.mu.Lock()
			if cnt := e.ejectedCount(e.maxNow); cnt > limit {
				e.mu.Unlock()
				t.Errorf("maxNow=%d: %d hosts ejected, limit %d", e.maxNow, cnt, limit)
				return
			}
			e.mu.Unlock()
		}
	}()
	writersWG.Wait()
	stop.Store(true)
	readerWG.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()
	if cnt := e.ejectedCount(e.maxNow); cnt > limit {
		t.Fatalf("maxNow=%d: %d hosts ejected, limit %d", e.maxNow, cnt, limit)
	}
	for h := range e.hosts {
		st := e.hosts[h]
		if st.c < 0 || st.e < 0 {
			t.Fatalf("host %d: negative state c=%d e=%d", h, st.c, st.e)
		}
		if len(st.win) > wn {
			t.Fatalf("host %d: window length %d > Wn=%d", h, len(st.win), wn)
		}
	}
}
