// Package ejector implements a host outlier ejector with a maximum ejection
// ratio, a sliding result window and out-of-order batch reporting.
package ejector

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Limits shared by every validation path.
const (
	maxBaseDuration = int64(1_000_000_000)         // B and Cap upper bound
	maxTime         = int64(1_000_000_000_000_000) // now upper bound
	maxWindow       = 64
)

// Rejection reasons. Every rejected call returns one of these (possibly
// wrapped with the offending batch index) and leaves all state untouched.
var (
	// ErrHostOutOfRange is returned when host is not in [0, N).
	ErrHostOutOfRange = errors.New("host out of range")
	// ErrInvalidTime is returned when now is negative or greater than 1e15.
	ErrInvalidTime = errors.New("invalid time")
	// ErrClockRegression is returned when now is smaller than the maximum
	// time already accepted by Report/ReportBatch.
	ErrClockRegression = errors.New("clock regression")
	// ErrInvalidConfig is returned by NewEjector for invalid parameters.
	ErrInvalidConfig = errors.New("invalid config")
)

// Result is the outcome of an accepted report.
type Result int

const (
	// Recorded means the report was recorded without triggering ejection.
	Recorded Result = iota
	// Ignored means the host is currently ejected; nothing changed.
	Ignored
	// Ejected means the report triggered an ejection that was allowed.
	Ejected
	// Capped means an ejection was triggered but blocked by the ratio cap.
	Capped
)

func (r Result) String() string {
	switch r {
	case Recorded:
		return "Recorded"
	case Ignored:
		return "Ignored"
	case Ejected:
		return "Ejected"
	case Capped:
		return "Capped"
	default:
		return "Unknown"
	}
}

// Event is a single out-of-order report inside a batch.
type Event struct {
	Host int
	OK   bool
	Now  int64
}

type hostState struct {
	c   int64  // consecutive failures
	e   int64  // accumulated ejection count
	u   int64  // ejected until (exclusive); recovered exactly at u
	win []bool // recent recorded reports, oldest first; true = success
}

// ejected reports whether the host is ejected at time now. Recovery needs
// no trigger: exactly at u the host is healthy again.
func (h *hostState) ejected(now int64) bool { return h.u > now }

// push appends one recorded outcome to the sliding window, dropping the
// oldest entry when the window exceeds Wn.
func (h *hostState) push(ok bool, wn int) {
	h.win = append(h.win, ok)
	if len(h.win) > wn {
		copy(h.win, h.win[1:])
		h.win = h.win[:wn]
	}
}

// failures counts the failures currently inside the window.
func (h *hostState) failures() int64 {
	var f int64
	for _, ok := range h.win {
		if !ok {
			f++
		}
	}
	return f
}

// Ejector tracks per-host health and ejects outlier hosts. All methods are
// safe for concurrent use; the result is equivalent to some serial order.
type Ejector struct {
	mu sync.Mutex

	n     int
	k     int64
	b     int64
	cap   int64
	p     int64
	wn    int
	q     int64
	hosts []hostState

	maxNow int64 // maximum now accepted so far, initial 0
}

// NewEjector validates the configuration and returns an Ejector, or
// ErrInvalidConfig (wrapped with the offending parameter) if any of:
// N, K, B < 1; B > 1e9; Cap < B or Cap > 1e9; P outside [0, 100];
// Wn outside [1, 64]; Q outside [1, 100].
func NewEjector(n int, k int64, b, capDur int64, p, wn, q int) (*Ejector, error) {
	if n < 1 {
		return nil, fmt.Errorf("%w: N=%d < 1", ErrInvalidConfig, n)
	}
	if k < 1 {
		return nil, fmt.Errorf("%w: K=%d < 1", ErrInvalidConfig, k)
	}
	if b < 1 || b > maxBaseDuration {
		return nil, fmt.Errorf("%w: B=%d outside [1, 1e9]", ErrInvalidConfig, b)
	}
	if capDur < b || capDur > maxBaseDuration {
		return nil, fmt.Errorf("%w: Cap=%d outside [B, 1e9]", ErrInvalidConfig, capDur)
	}
	if p < 0 || p > 100 {
		return nil, fmt.Errorf("%w: P=%d outside [0, 100]", ErrInvalidConfig, p)
	}
	if wn < 1 || wn > maxWindow {
		return nil, fmt.Errorf("%w: Wn=%d outside [1, 64]", ErrInvalidConfig, wn)
	}
	if q < 1 || q > 100 {
		return nil, fmt.Errorf("%w: Q=%d outside [1, 100]", ErrInvalidConfig, q)
	}
	return &Ejector{
		n:     n,
		k:     k,
		b:     b,
		cap:   capDur,
		p:     int64(p),
		wn:    wn,
		q:     int64(q),
		hosts: make([]hostState, n),
	}, nil
}

// checkHost validates the host index.
func (e *Ejector) checkHost(host int) error {
	if host < 0 || host >= e.n {
		return fmt.Errorf("%w: host=%d, N=%d", ErrHostOutOfRange, host, e.n)
	}
	return nil
}

// checkTime validates the timestamp range.
func checkTime(now int64) error {
	if now < 0 || now > maxTime {
		return fmt.Errorf("%w: now=%d outside [0, 1e15]", ErrInvalidTime, now)
	}
	return nil
}

// checkClock validates now against the maximum accepted time.
func (e *Ejector) checkClock(now int64) error {
	if now < e.maxNow {
		return fmt.Errorf("%w: now=%d < maxNow=%d", ErrClockRegression, now, e.maxNow)
	}
	return nil
}

// ejectedCount returns how many hosts are ejected at time now.
func (e *Ejector) ejectedCount(now int64) int64 {
	var cnt int64
	for i := range e.hosts {
		if e.hosts[i].ejected(now) {
			cnt++
		}
	}
	return cnt
}

// ejectDuration is min(Cap, B*e) computed without overflow: B >= 1 and
// Cap <= 1e9, so e > Cap/B already implies B*e > Cap.
func (e *Ejector) ejectDuration(count int64) int64 {
	if count > e.cap/e.b {
		return e.cap
	}
	if d := e.b * count; d < e.cap {
		return d
	}
	return e.cap
}

// reportLocked applies the Report rules; the caller holds the lock and has
// already validated host, time range and clock. It advances maxNow.
func (e *Ejector) reportLocked(host int, ok bool, now int64) Result {
	e.maxNow = now
	h := &e.hosts[host]

	if h.ejected(now) {
		return Ignored
	}

	if ok {
		h.c = 0
		if h.e > 0 {
			h.e--
		}
		h.push(true, e.wn)
		return Recorded
	}

	h.c++
	h.push(false, e.wn)

	trigger := h.c >= e.k
	if !trigger && len(h.win) == e.wn {
		// Window failure rate: f*100 >= Q*Wn, pure integer comparison.
		trigger = h.failures()*100 >= e.q*int64(e.wn)
	}
	if !trigger {
		return Recorded
	}

	// Ratio cap: allow only when (E+1)*100 <= P*N, integer comparison.
	if (e.ejectedCount(now)+1)*100 > e.p*int64(e.n) {
		return Capped
	}
	h.e++
	h.u = now + e.ejectDuration(h.e)
	h.c = 0
	h.win = h.win[:0]
	return Ejected
}

// Report records a single outcome for host at time now. Rejections are
// checked in order: host range, time range, clock regression; a rejected
// report changes nothing. Accepted reports advance the maximum seen time.
func (e *Ejector) Report(host int, ok bool, now int64) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.checkHost(host); err != nil {
		return 0, err
	}
	if err := checkTime(now); err != nil {
		return 0, err
	}
	if err := e.checkClock(now); err != nil {
		return 0, err
	}
	return e.reportLocked(host, ok, now), nil
}

// ReportBatch atomically applies a batch of out-of-order events. It first
// validates every event in input order (host range, then time range, first
// problem wins), then checks the batch minimum now against the maximum
// accepted time. Any failure rejects the whole batch with no state change.
// Otherwise events are stably sorted by now (equal now keeps input order),
// applied one by one under the Report rules, and the returned results are
// aligned with the input order. An empty batch returns an empty slice.
func (e *Ejector) ReportBatch(events []Event) ([]Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	results := make([]Result, len(events))
	if len(events) == 0 {
		return results, nil
	}

	for i, ev := range events {
		if err := e.checkHost(ev.Host); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}
		if err := checkTime(ev.Now); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}
	}

	minNow := events[0].Now
	for _, ev := range events[1:] {
		if ev.Now < minNow {
			minNow = ev.Now
		}
	}
	if err := e.checkClock(minNow); err != nil {
		return nil, err
	}

	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return events[order[a]].Now < events[order[b]].Now
	})
	for _, idx := range order {
		ev := events[idx]
		results[idx] = e.reportLocked(ev.Host, ev.OK, ev.Now)
	}
	return results, nil
}

// Ejected reports whether host is ejected at time now. It performs the same
// rejection checks as Report but never advances the maximum accepted time.
func (e *Ejector) Ejected(host int, now int64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.checkHost(host); err != nil {
		return false, err
	}
	if err := checkTime(now); err != nil {
		return false, err
	}
	if err := e.checkClock(now); err != nil {
		return false, err
	}
	return e.hosts[host].ejected(now), nil
}

// Healthy returns the ascending list of hosts not ejected at time now. It
// performs the same rejection checks as Report (except host range) but
// never advances the maximum accepted time.
func (e *Ejector) Healthy(now int64) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := checkTime(now); err != nil {
		return nil, err
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	healthy := make([]int, 0, e.n)
	for i := range e.hosts {
		if !e.hosts[i].ejected(now) {
			healthy = append(healthy, i)
		}
	}
	return healthy, nil
}
