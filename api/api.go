// Package api is the public entry point for deterministic CDC replay with
// last-write-wins compaction. It depends only on lww, which depends on seq.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/lww"
	"ontology/seq"
)

// Change is one incoming CDC change: {Key, Ver, Val}.
type Change = seq.Change

// Record is one compacted (Key, Val) pair in Replay output.
type Record = lww.Record

// Sentinel errors are mutually distinct and decidable with errors.Is.
var (
	ErrEmptyKey        = errors.New("api: change key must not be empty")
	ErrNegativeVer     = errors.New("api: change version must be >= 0")
	ErrHistoryOverflow = errors.New("api: per-key history exceeds maxHistory")
)

func chg(key string, ver, val int64) Change { return Change{Key: key, Ver: ver, Val: val} }
func rec(key string, val int64) Record      { return Record{Key: key, Val: val} }

// Engine feeds changes and answers deterministic compacted reads.
type Engine struct {
	mu         sync.RWMutex
	store      *seq.Store
	tab        *lww.Table
	maxHistory int
}

// New creates an Engine allowing at most maxHistory stored changes per key.
func New(maxHistory int) *Engine {
	return &Engine{store: seq.New(), tab: lww.NewTable(), maxHistory: maxHistory}
}

// Feed applies one batch atomically. The whole batch is validated first; if any
// change is rejected nothing is applied: no SN consumed, no history or winner
// changed. On success changes commit in slice order, which is the SN order.
func (e *Engine) Feed(chgs []Change) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	add := map[string]int{}
	for _, c := range chgs { // content validation, fixed order
		if c.Key == "" {
			return ErrEmptyKey
		}
		if c.Ver < 0 {
			return ErrNegativeVer
		}
		add[c.Key]++
	}
	for k, n := range add { // capacity validation (map order cannot change the result)
		if e.store.Count(k)+n > e.maxHistory {
			return ErrHistoryOverflow
		}
	}
	for _, c := range chgs { // commit phase, only when every change is valid
		sn := e.store.Next()
		e.store.Append(sn, c)
		e.tab.Apply(seq.Stored{Change: c, SN: sn})
	}
	return nil
}

// Replay returns all winners sorted by Key ascending. It is a pure read: the
// slice is freshly allocated and a second call returns byte-identical output.
func (e *Engine) Replay() []Record {
	e.mu.RLock()
	out := e.tab.Snapshot()
	e.mu.RUnlock()
	return out
}

// History returns every change stored for key in ascending SN (arrival) order.
func (e *Engine) History(key string) []Change {
	e.mu.RLock()
	stored := e.store.History(key)
	e.mu.RUnlock()
	out := make([]Change, len(stored))
	for i, s := range stored {
		out[i] = s.Change
	}
	return out
}

// demoSeq is the built-in seven-change sequence used by SelfCheck and the demo.
var demoSeq = []Change{
	chg("a", 10, 100), chg("b", 5, 50), chg("a", 10, 200), chg("c", 7, 70),
	chg("a", 5, 50), chg("b", 12, 90), chg("c", 7, 77),
}

// SelfCheck replays demoSeq on a private engine and verifies the four
// invariants: reference agreement, deterministic pure reads, stable stepwise
// winners, and no-trace rejection. It never mutates the receiver.
func (e *Engine) SelfCheck() error {
	eng := New(100)
	want := []Record{rec("a", 200), rec("b", 90), rec("c", 77)}
	steps := [][3]int64{{100, -1, -1}, {100, 50, -1}, {200, 50, -1}, {200, 50, 70},
		{200, 50, 70}, {200, 90, 70}, {200, 90, 77}} // winner Val for a/b/c; -1 = absent
	for i, c := range demoSeq {
		if err := eng.Feed([]Change{c}); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		have := map[string]int64{}
		for _, r := range eng.Replay() {
			have[r.Key] = r.Val
		}
		for j, k := range [3]string{"a", "b", "c"} {
			w := steps[i][j]
			if v, ok := have[k]; (w == -1 && ok) || (w != -1 && v != w) {
				return fmt.Errorf("selfcheck step %d key %s = %d(ok=%v), want %d", i+1, k, v, ok, w)
			}
		}
	}
	if got := eng.Replay(); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("selfcheck replay %v, want %v", got, want)
	}
	r1, h1 := fmt.Sprint(eng.Replay()), len(eng.History("a"))
	if r1 != fmt.Sprint(eng.Replay()) || len(eng.History("a")) != h1 {
		return errors.New("selfcheck: replay is not a state-free pure function")
	}
	bad := New(2)
	_ = bad.Feed([]Change{chg("x", 1, 1)})
	for _, b := range [][]Change{
		{chg("", 1, 1)},
		{chg("x", -1, 1)},
		{chg("x", 1, 1), chg("x", 1, 1)},
	} {
		before := fmt.Sprint(bad.Replay())
		if bad.Feed(b) == nil || fmt.Sprint(bad.Replay()) != before {
			return errors.New("selfcheck: a rejection was missing or left a trace")
		}
	}
	if err := bad.Feed([]Change{chg("y", 0, 0)}); err != nil {
		return fmt.Errorf("selfcheck: engine unusable after rejection: %w", err)
	}
	return nil
}
