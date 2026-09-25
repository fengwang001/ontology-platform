// Package mon maintains completion statistics and the sliding SLA alarm
// window. It depends only on sla. Its types are single-goroutine; the api
// package serializes every call.
package mon

import "ontology/sla"

// Window is a fixed-capacity ring buffer holding the verdicts of the most
// recent completions (true = violation), with a running violation count so
// each slide is O(1).
type Window struct {
	ring       []bool
	head       int // index of the oldest entry
	size       int // entries currently stored, 0 <= size <= w
	violations int // running number of violations currently in the window
	w, k       int
	// lastChecks is how many completion records were inspected while applying
	// the latest completion. With a ring buffer it is constant: the incoming
	// record, plus one evicted record when the window was full. It never
	// scales with the total number of completions and has no exported accessor.
	lastChecks int
}

// NewWindow creates a window of the w most recent completions breaching at k.
func NewWindow(w, k int) *Window {
	return &Window{ring: make([]bool, w), w: w, k: k}
}

// push slides one completion verdict into the window and records the number
// of completion records inspected during the update.
func (x *Window) push(violation bool) {
	checks := 1 // the incoming completion record
	if x.size < x.w {
		x.ring[x.size] = violation
		x.size++
	} else {
		checks++ // the evicted oldest record must be inspected once
		if x.ring[x.head] {
			x.violations--
		}
		x.ring[x.head] = violation
		x.head = (x.head + 1) % x.w
	}
	if violation {
		x.violations++
	}
	x.lastChecks = checks
}

// Breached reports whether violations in the current window reach k. With
// fewer than w completions, all completions so far are the window.
func (x *Window) Breached() bool { return x.violations >= x.k }

// Monitor accumulates statistics over completed requests and owns the window.
type Monitor struct {
	threshold  int64
	count      int64
	violations int64
	sum        int64
	min        int64
	max        int64
	started    bool // whether at least one completion exists (min/max guard)
	inFlight   int
	win        *Window
}

// NewMonitor builds a Monitor with threshold T and alarm window W/K.
func NewMonitor(threshold int64, w, k int) *Monitor {
	return &Monitor{threshold: threshold, win: NewWindow(w, k)}
}

// Begin registers one in-flight request. In-flight requests enter no
// statistic until they Complete.
func (m *Monitor) Begin() { m.inFlight++ }

// Complete records one finished request: it leaves the in-flight set, enters
// the totals and the sliding window, and returns its sla.Verdict.
func (m *Monitor) Complete(latency int64) sla.Verdict {
	v := sla.Classify(latency, m.threshold)
	m.inFlight--
	m.count++
	m.sum += latency
	if !m.started {
		m.min, m.max, m.started = latency, latency, true
	} else {
		if latency < m.min {
			m.min = latency
		}
		if latency > m.max {
			m.max = latency
		}
	}
	if v == sla.Violation {
		m.violations++
	}
	m.win.push(v == sla.Violation)
	return v
}

func (m *Monitor) Count() int64      { return m.count }
func (m *Monitor) Violations() int64 { return m.violations }
func (m *Monitor) InFlight() int     { return m.inFlight }

// Min returns the smallest completed latency and false if none exists.
func (m *Monitor) Min() (int64, bool) { return m.min, m.started }

// Max returns the largest completed latency and false if none exists.
func (m *Monitor) Max() (int64, bool) { return m.max, m.started }

// Avg returns the mean completed latency; 0 when nothing has completed.
func (m *Monitor) Avg() float64 {
	if m.count == 0 {
		return 0
	}
	return float64(m.sum) / float64(m.count)
}

func (m *Monitor) Breached() bool { return m.win.Breached() }

// ConstantWindowUpdate reports whether the inspection count recorded while
// applying one completion stays at a fixed small constant across several
// total-completion tiers. It returns only the verdict: the unexported counter
// value itself never crosses the package boundary.
func ConstantWindowUpdate() bool {
	seen := -1
	for _, total := range []int{100, 1000, 10000} {
		m := NewMonitor(10, 4, 3)
		for i := 0; i < total; i++ {
			m.Begin()
			m.Complete(int64(i % 20))
		}
		m.Begin()
		m.Complete(5)
		c := m.win.lastChecks
		if c > 2 {
			return false
		}
		if seen == -1 {
			seen = c
		} else if c != seen {
			return false
		}
	}
	return true
}
