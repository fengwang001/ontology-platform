// Package api is the external entry point of the batch event-time aligner.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/lww"
)

// Event is one upstream change.
type Event = lww.Event

const noBound = int64(1<<63 - 1) // "no aligned time / no lower bound"

// V is the aligner handle to feed events to and read results from.
type V struct {
	mu  sync.RWMutex
	r   *lww.Register
	max int
}

// The four rejection causes are distinct, decidable sentinel errors.
var ErrInvalidParam = errors.New("api: maxBatchEvents must be positive")
var ErrInvalidEvent = errors.New("api: event key must not be empty")
var ErrNoOpenBatch = errors.New("api: Feed called with no open batch")
var ErrBatchTooLarge = errors.New("api: accepted event count would exceed maxBatchEvents")

// New builds an aligner whose batches hold at most maxBatchEvents accepted events.
func New(maxBatchEvents int) (*V, error) {
	if maxBatchEvents <= 0 {
		return nil, ErrInvalidParam
	}
	return &V{r: lww.New(), max: maxBatchEvents}, nil
}

// BeginBatch closes the open batch (if any) and opens the next one.
func (v *V) BeginBatch() { v.mu.Lock(); v.r.Begin(); v.mu.Unlock() }

// Feed accepts one event or rejects it without changing any state (invariant 4).
func (v *V) Feed(ev Event) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	al := v.r.Aligner()
	if !al.Open() {
		return ErrNoOpenBatch
	}
	if ev.Key == "" {
		return ErrInvalidEvent
	}
	if al.WouldAccept(ev.TS) && al.Count() >= v.max {
		return ErrBatchTooLarge
	}
	v.r.Feed(ev) // accepted, or late -> dropped inside lww
	return nil
}

// EndBatch closes the open batch.
func (v *V) EndBatch() { v.mu.Lock(); v.r.Close(); v.mu.Unlock() }

// Flush closes the open batch; afterwards all fed events are applied.
func (v *V) Flush() { v.mu.Lock(); v.r.Close(); v.mu.Unlock() }

// Value returns the current final value for key and whether it exists.
func (v *V) Value(key string) (int, bool) { v.mu.RLock(); defer v.mu.RUnlock(); return v.r.Value(key) }

// AlignedTimes returns a copy of per-batch aligned times (noBound=none).
func (v *V) AlignedTimes() []int64 { v.mu.RLock(); defer v.mu.RUnlock(); return v.r.Aligned() }

// Dropped returns the count of late, dropped events.
func (v *V) Dropped() int { v.mu.RLock(); defer v.mu.RUnlock(); return v.r.Dropped() }

// recompute is the independent batch definition used to pin invariants 1-3.
func recompute(plan [][]Event) (map[string]int, []int64, int) {
	ts, out := map[string]int64{}, map[string]int{}
	var aligned []int64
	prev, dropped := noBound, 0
	for _, batch := range plan {
		m := noBound
		for _, e := range batch {
			if prev != noBound && e.TS < prev {
				dropped++
				continue
			}
			if e.TS < m {
				m = e.TS
			}
			if cur, ok := ts[e.Key]; !ok || e.TS >= cur {
				ts[e.Key], out[e.Key] = e.TS, e.V
			}
		}
		if m == noBound {
			m = prev
		}
		aligned, prev = append(aligned, m), m
	}
	return out, aligned, dropped
}

// SelfCheck replays the built-in section-3 sequence, verifying invariants 1-4.
func (v *V) SelfCheck() error {
	plan := [][]Event{
		{{Key: "k", TS: 10, V: 1}, {Key: "k", TS: 5, V: 2}}, {{Key: "k", TS: 20, V: 3}, {Key: "k", TS: 12, V: 4}},
		{{Key: "k", TS: 8, V: 5}, {Key: "k", TS: 12, V: 6}}, {{Key: "k", TS: 25, V: 7}, {Key: "k", TS: 25, V: 9}},
	}
	g, _ := New(8)
	for _, b := range plan {
		g.BeginBatch()
		for _, e := range b {
			g.Feed(e) // cannot fail: non-empty keys, capacity 8
		}
		g.Flush()
	}
	rv, ra, rd := recompute(plan)
	at := g.AlignedTimes()
	got, _ := g.Value("k")
	ok := got == 9 && rv["k"] == 9 && g.Dropped() == rd && rd == 1 && reflect.DeepEqual(at, ra)
	for i := 1; ok && i < len(at); i++ {
		ok = at[i] >= at[i-1]
	}
	if !ok {
		return fmt.Errorf("invariants 1-3: value=%d dropped=%d aligned=%v", got, g.Dropped(), at)
	}
	return selfCheckRejections()
}

func selfCheckRejections() error {
	_, errParam := New(0)
	idle, _ := New(2)
	errNoBatch := idle.Feed(Event{Key: "k", TS: 1})
	if !errors.Is(errParam, ErrInvalidParam) || !errors.Is(errNoBatch, ErrNoOpenBatch) || idle.Dropped() != 0 {
		return fmt.Errorf("invariant 4: %v %v dropped=%d", errParam, errNoBatch, idle.Dropped())
	}
	z, _ := New(1)
	z.BeginBatch()
	errKey := z.Feed(Event{Key: "", TS: 1})
	errOK := z.Feed(Event{Key: "k", TS: 5, V: 5})
	errCap := z.Feed(Event{Key: "k", TS: 6, V: 6})
	got, _ := z.Value("k")
	if !errors.Is(errKey, ErrInvalidEvent) || errOK != nil || !errors.Is(errCap, ErrBatchTooLarge) {
		return fmt.Errorf("invariant 4: %v %v %v", errKey, errOK, errCap)
	}
	if got != 5 || z.Dropped() != 0 {
		return fmt.Errorf("invariant 4: state changed v=%d dropped=%d", got, z.Dropped())
	}
	return nil
}
