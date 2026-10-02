// Package overagg implements an event-time OVER range aggregator.
//
// Rows are buffered until the watermark passes their event timestamp, then
// released in (ts, seq) order. Each released row is emitted with the
// aggregate (sum, count, max) of its key's rows inside the range frame
// [ts-R, ts]. Rows arriving after the watermark but within the allowed
// lateness are reissued immediately; older rows are dropped as late.
package overagg

import (
	"errors"
	"sort"
	"sync"
)

const (
	// MaxParam is the inclusive upper bound for R, AL, ts and w.
	MaxParam = int64(1_000_000_000_000_000)
	// MaxVal is the inclusive upper bound for |val|.
	MaxVal = int64(1_000_000_000)
	// MaxCap is the inclusive upper bound for the buffer capacity.
	MaxCap = int64(1_000_000)
)

var (
	// ErrInvalidParam reports an out-of-range constructor or call argument.
	ErrInvalidParam = errors.New("overagg: invalid parameter")
	// ErrWatermarkRegression reports an Advance watermark below the current one.
	ErrWatermarkRegression = errors.New("overagg: watermark regression")
)

// Output is one emitted row together with its frame aggregates.
type Output struct {
	Key []byte
	Ts  int64
	Val int64
	Sum int64
	Cnt int64
	Max int64
}

// InsertResult classifies the outcome of an Insert call.
type InsertResult int

const (
	// InsertInvalid: rejected, a parameter was out of range.
	InsertInvalid InsertResult = iota
	// InsertLate: dropped, ts <= wm-AL.
	InsertLate
	// InsertReissued: emitted immediately, wm-AL < ts <= wm.
	InsertReissued
	// InsertBuffered: stored in the buffer, ts > wm.
	InsertBuffered
	// InsertFull: rejected, the buffer already holds Cap rows.
	InsertFull
)

func (r InsertResult) String() string {
	switch r {
	case InsertInvalid:
		return "invalid"
	case InsertLate:
		return "late"
	case InsertReissued:
		return "reissued"
	case InsertBuffered:
		return "buffered"
	case InsertFull:
		return "full"
	}
	return "unknown"
}

// Stats counts Insert outcomes. The six classes are disjoint and their sum
// equals the total number of Insert calls.
type Stats struct {
	Invalid  int64 // rejected: invalid parameter
	Full     int64 // rejected: buffer full
	Late     int64 // dropped as late
	Reissued int64 // reissued immediately
	Buffered int64 // still in the buffer
	Released int64 // released by Advance (including already cleaned rows)
}

// Aggregator is safe for concurrent use; every method is linearizable and
// Advance is a single atomic step.
type Aggregator struct {
	mu  sync.Mutex
	r   int64
	al  int64
	cap int64

	wm  int64
	seq int64
	buf []bufRow

	keys     map[string]*keyState
	retained int64

	invalidCount  int64
	fullCount     int64
	lateCount     int64
	reissueCount  int64
	releasedCount int64

	frameWork int64
	lateWork  int64
}

type bufRow struct {
	key string
	ts  int64
	val int64
	seq int64
}

// New builds an aggregator with range width r, allowed lateness al and
// buffer capacity cap. It returns ErrInvalidParam when r or al fall outside
// [0, 1e15] or cap falls outside [1, 1e6].
func New(r, al, cap int64) (*Aggregator, error) {
	if r < 0 || r > MaxParam || al < 0 || al > MaxParam || cap < 1 || cap > MaxCap {
		return nil, ErrInvalidParam
	}
	return &Aggregator{
		r:    r,
		al:   al,
		cap:  cap,
		wm:   -1,
		keys: make(map[string]*keyState),
	}, nil
}

// Insert classifies the row and applies the matching action, in the order:
// invalid parameter, late drop, reissue, buffer full, buffered.
func (a *Aggregator) Insert(key []byte, ts, val int64) (InsertResult, Output) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(key) == 0 || ts < 0 || ts > MaxParam || val < -MaxVal || val > MaxVal {
		a.invalidCount++
		return InsertInvalid, Output{}
	}
	if ts <= a.wm-a.al {
		a.lateCount++
		return InsertLate, Output{}
	}
	if ts <= a.wm {
		return a.reissue(key, ts, val)
	}
	if int64(len(a.buf)) >= a.cap {
		a.fullCount++
		return InsertFull, Output{}
	}
	a.seq++
	a.buf = append(a.buf, bufRow{key: string(key), ts: ts, val: val, seq: a.seq})
	return InsertBuffered, Output{}
}

// reissue emits one row immediately against the retained released rows.
// The caller must hold a.mu and must have classified the row as a reissue.
func (a *Aggregator) reissue(key []byte, ts, val int64) (InsertResult, Output) {
	k := string(key)
	ks := a.keys[k]
	if ks == nil {
		ks = newKeyState()
		a.keys[k] = ks
	}
	sum, cnt, max, ok := ks.frameQuery(a, ts)
	sum += val
	cnt++
	if !ok || val > max {
		max = val
	}
	ks.addRow(a, ts, val)
	a.retained++
	a.reissueCount++
	out := Output{Key: append([]byte(nil), key...), Ts: ts, Val: val, Sum: sum, Cnt: cnt, Max: max}
	return InsertReissued, out
}

// Advance releases every buffered row with ts <= w in (ts, seq) order,
// moves the watermark to w and cleans retained rows with ts' <= w-R-AL.
func (a *Aggregator) Advance(w int64) ([]Output, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if w < 0 || w > MaxParam {
		return nil, ErrInvalidParam
	}
	if w < a.wm {
		return nil, ErrWatermarkRegression
	}
	if w == a.wm {
		return nil, nil
	}

	rel := make([]bufRow, 0, len(a.buf))
	keep := a.buf[:0]
	for _, r := range a.buf {
		if r.ts <= w {
			rel = append(rel, r)
		} else {
			keep = append(keep, r)
		}
	}
	a.buf = keep
	sort.Slice(rel, func(i, j int) bool {
		if rel[i].ts != rel[j].ts {
			return rel[i].ts < rel[j].ts
		}
		return rel[i].seq < rel[j].seq
	})

	var outs []Output
	for i := 0; i < len(rel); {
		j := i
		for j < len(rel) && rel[j].ts == rel[i].ts {
			j++
		}
		group := rel[i:j]
		t := rel[i].ts

		seen := make(map[string]bool, len(group))
		var groupKeys []string
		for _, r := range group {
			ks := a.keys[r.key]
			if ks == nil {
				ks = newKeyState()
				a.keys[r.key] = ks
			}
			ks.addRow(a, r.ts, r.val)
			a.retained++
			if !seen[r.key] {
				seen[r.key] = true
				groupKeys = append(groupKeys, r.key)
			}
		}
		for _, k := range groupKeys {
			a.keys[k].advanceWindow(a, t)
		}
		for _, r := range group {
			ks := a.keys[r.key]
			outs = append(outs, Output{
				Key: []byte(r.key),
				Ts:  r.ts,
				Val: r.val,
				Sum: ks.winSum,
				Cnt: ks.winCnt,
				Max: ks.maxQuery(a),
			})
		}
		a.releasedCount += int64(len(group))
		i = j
	}

	a.wm = w
	limit := w - a.r - a.al
	for k, ks := range a.keys {
		ks.cleanup(a, limit)
		if len(ks.buckets) == 0 {
			delete(a.keys, k)
		}
	}
	return outs, nil
}

// WM returns the current watermark.
func (a *Aggregator) WM() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wm
}

// Buffered returns the number of rows currently in the buffer.
func (a *Aggregator) Buffered() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.buf)
}

// Retained returns the number of released rows kept after cleanup.
func (a *Aggregator) Retained() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.retained
}

// Seq returns the last assigned global arrival sequence number, i.e. how
// many rows have been buffered so far.
func (a *Aggregator) Seq() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.seq
}

// Stats returns the Insert outcome counters.
func (a *Aggregator) Stats() Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Stats{
		Invalid:  a.invalidCount,
		Full:     a.fullCount,
		Late:     a.lateCount,
		Reissued: a.reissueCount,
		Buffered: int64(len(a.buf)),
		Released: a.releasedCount,
	}
}
