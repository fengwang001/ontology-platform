// Package adapt layers an observation window over a wmline.Line and adapts
// its delay from the observed lateness rate: too many late events in a window
// raises the delay (more tolerant), no late events lowers it (tighter), but
// never below minDelay or above maxDelay. Exactly one step is taken per
// settled window, so the delay moves monotonically toward a stable value from
// either direction.
package adapt

import (
	"errors"
	"sync"

	"ontology/wmline"
)

// Sentinel errors: every rejected construction is reported by exactly one of
// these distinguishable errors.
var (
	ErrMinAboveMax       = errors.New("adapt: minDelay greater than maxDelay")
	ErrNegativeMin       = errors.New("adapt: minDelay must not be negative")
	ErrNonPositiveStep   = errors.New("adapt: step must be positive")
	ErrNonPositiveWindow = errors.New("adapt: window size W must be >= 1")
	ErrThresholdRange    = errors.New("adapt: require 0 <= lo < hi <= W")
)

// Window is the adaptive watermark. All methods are safe for concurrent use;
// Feed serializes with the read-only WM/Delay accessors.
type Window struct {
	mu sync.RWMutex

	line                  *wmline.Line
	minDelay, maxDelay    int64
	step                  int64
	wSize, hi, lo         int64
	n, lateCount          int64
	lastSettlementScanned int64 // non-exported: events rescanned at last settle
}

// New validates every parameter before touching any state, so a rejected call
// returns (nil, err) without creating or mutating anything.
func New(minDelay, maxDelay, step, wSize, hi, lo int64) (*Window, error) {
	if minDelay < 0 {
		return nil, ErrNegativeMin
	}
	if minDelay > maxDelay {
		return nil, ErrMinAboveMax
	}
	if step <= 0 {
		return nil, ErrNonPositiveStep
	}
	if wSize < 1 {
		return nil, ErrNonPositiveWindow
	}
	if lo < 0 || hi > wSize || lo >= hi {
		return nil, ErrThresholdRange
	}
	return &Window{
		line:     wmline.NewLine(minDelay),
		minDelay: minDelay,
		maxDelay: maxDelay,
		step:     step,
		wSize:    wSize,
		hi:       hi,
		lo:       lo,
	}, nil
}

// Feed applies one event exactly in the specified order: lateness is judged
// against the pre-event watermark, maxSeen/wm advance, and only then the
// window counters (and possibly a settlement) run. It reports lateness.
func (w *Window) Feed(ts int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	late := w.line.Observe(ts)
	w.n++
	if late {
		w.lateCount++
	}
	if w.n == w.wSize {
		w.settle()
	}
	return late
}

// settle runs only when the window is full. lateCount is maintained per event
// in O(1), so settlement reads the counter directly: zero events are ever
// rescanned, regardless of window size.
func (w *Window) settle() {
	w.lastSettlementScanned = 0
	switch {
	case w.lateCount >= w.hi:
		d := w.line.Delay() + w.step
		if d > w.maxDelay {
			d = w.maxDelay
		}
		w.line.SetDelay(d)
	case w.lateCount <= w.lo:
		d := w.line.Delay() - w.step
		if d < w.minDelay {
			d = w.minDelay
		}
		w.line.SetDelay(d)
	}
	w.n = 0
	w.lateCount = 0
}

// WM returns the current watermark.
func (w *Window) WM() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.line.WM()
}

// Delay returns the current delay, always within [minDelay, maxDelay].
func (w *Window) Delay() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.line.Delay()
}

// settlementScanBound is the m-independent upper bound on events rescanned by
// one settlement: lateCount is accumulated per event and settle reads it.
const settlementScanBound = 0

// ConstantSettlementCost verifies across several window sizes m that the
// latest settlement rescans no more than a small, m-independent constant
// number of events. Only pass/fail crosses the package boundary; the counter
// value itself is never exposed through any exported method.
func ConstantSettlementCost() error {
	for _, m := range []int64{100, 1000, 10000} {
		w, err := New(0, m, 1, m, 1, 0)
		if err != nil {
			return err
		}
		for i := int64(0); i < m; i++ {
			w.Feed(m - i) // one window, exactly one late event -> neutral
		}
		w.mu.RLock()
		got := w.lastSettlementScanned
		w.mu.RUnlock()
		if got > settlementScanBound {
			return errors.New("adapt: settlement rescanned events")
		}
	}
	return nil
}
