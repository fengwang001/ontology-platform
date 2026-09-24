// Package api is the public cumulative-window counter API.
package api

import (
	"errors"
	"reflect"
	"sort"
	"strconv"
	"sync"

	"ontology/cagg"
	"ontology/cwin"
)

type (
	Event = cagg.Event
	Out   = cagg.Out
)

var (
	ErrInvalidParams  = cagg.ErrInvalidParams
	ErrTooManyWindows = cagg.ErrTooManyWindows
	ErrEmptyKey       = cagg.ErrEmptyKey
	errSelfCheck      = errors.New("api: self-check failed")
)

type Window struct {
	mu  sync.RWMutex
	agg *cagg.Agg
}

func New(max, step, delay int64, maxOpen int) (*Window, error) {
	a, err := cagg.New(max, step, delay, maxOpen)
	if err != nil {
		return nil, err
	}
	return &Window{agg: a}, nil
}
func (w *Window) Feed(evs []Event) ([]Out, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.agg.Feed(evs)
}
func (w *Window) Flush() []Out   { w.mu.Lock(); defer w.mu.Unlock(); return w.agg.Flush() }
func (w *Window) All() []Out     { w.mu.RLock(); defer w.mu.RUnlock(); return w.agg.All() }
func (w *Window) Dropped() int64 { w.mu.RLock(); defer w.mu.RUnlock(); return w.agg.Dropped() }

// SelfCheck replays the built-in sequence and verifies the four invariants.
func (w *Window) SelfCheck() error {
	const M, step, delay int64 = 12, 4, 2
	a, err := cagg.New(M, step, delay, 2)
	if err != nil {
		return err
	}
	accepted, maxTS := []Event(nil), int64(0)
	for i, ts := range []int64{-5, 1, -2, 6, 3, 11, 12, 19} {
		if i == 0 || ts > maxTS {
			maxTS = ts
		}
		wm := maxTS - delay
		outs, ferr := a.Feed([]Event{{Key: "K", TS: ts}})
		if ferr != nil {
			return ferr
		}
		for _, o := range outs { // invariant 3: never fire ahead of wm
			if o.End > wm {
				return errSelfCheck
			}
		}
		s := cwin.Start(ts, M)
		if emin := cwin.End(s, step, cwin.MinSub(ts, s, step)); wm < emin {
			accepted = append(accepted, Event{Key: "K", TS: ts})
		}
	}
	if a.Dropped() != 1 || !monotone(a.All()) { // invariant 2; TS=3 sole drop
		return errSelfCheck
	}
	a.Flush()
	if !reflect.DeepEqual(sortOuts(a.All()), batchRecompute(accepted, M, step)) {
		return errSelfCheck // invariant 1
	}
	return errors.Join(rejectionLeavesNoTrace(), cagg.VerifyScanBound()) // invariant 4 + complexity
}
func rejectionLeavesNoTrace() error {
	if _, err := cagg.New(12, 5, 2, 2); !errors.Is(err, cagg.ErrInvalidParams) {
		return errSelfCheck
	}
	a, _ := cagg.New(12, 4, 2, 1)
	if _, err := a.Feed([]Event{{Key: "K", TS: -5}}); err != nil {
		return err
	}
	before, drop := a.All(), a.Dropped()
	if _, err := a.Feed([]Event{{Key: "K", TS: 1}}); !errors.Is(err, cagg.ErrTooManyWindows) {
		return errSelfCheck
	}
	if _, err := a.Feed([]Event{{Key: "", TS: 1}}); !errors.Is(err, cagg.ErrEmptyKey) {
		return errSelfCheck
	}
	if !reflect.DeepEqual(a.All(), before) || a.Dropped() != drop {
		return errSelfCheck
	}
	return nil
}

// batchRecompute is the independent oracle for invariant 1.
func batchRecompute(evs []Event, max, step int64) []Out {
	g, st, ky := map[string][]int64{}, map[string]int64{}, map[string]string{}
	var ids []string
	for _, e := range evs {
		id := e.Key + "/" + strconv.FormatInt(cwin.Start(e.TS, max), 10)
		if g[id] == nil {
			ids, st[id], ky[id] = append(ids, id), cwin.Start(e.TS, max), e.Key
		}
		g[id] = append(g[id], e.TS)
	}
	var res []Out
	for _, id := range ids {
		for j := 1; j <= cwin.SubCount(max, step); j++ {
			var c int64
			for _, ts := range g[id] {
				if ts < cwin.End(st[id], step, j) {
					c++
				}
			}
			res = append(res, Out{Key: ky[id], Start: st[id], End: cwin.End(st[id], step, j), Count: c})
		}
	}
	return sortOuts(res)
}
func sortOuts(o []Out) []Out {
	c := append([]Out{}, o...)
	sort.Slice(c, func(i, j int) bool {
		x, y := c[i], c[j]
		return x.Start < y.Start || x.Start == y.Start &&
			(x.End < y.End || x.End == y.End && x.Key < y.Key)
	})
	return c
}
func monotone(all []Out) bool { // invariant 2 within one (Key, large window)
	prev := map[[2]any]int64{}
	for _, o := range all {
		k := [2]any{o.Key, o.Start}
		if p, ok := prev[k]; ok && o.Count < p {
			return false
		}
		prev[k] = o.Count
	}
	return true
}
