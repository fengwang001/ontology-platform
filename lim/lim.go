// Package lim implements backpressure and fairness on top of tb: a strict
// FIFO waiting queue, admission-time derivation, Feed and Dropped.
package lim

import (
	"errors"
	"math"
	"sync"

	"ontology/tb"
)

var (
	ErrTSRollback   = errors.New("lim: event TS went backwards")
	ErrEmptyKey     = errors.New("lim: event key must not be empty")
	ErrInvalidParam = tb.ErrInvalidParam
)

// Never is the admission tick of an event that waits forever (rate 0 after
// the burst is exhausted): backpressure keeps it, it is never dropped.
const Never = int64(math.MaxInt64)

// Event is a change-stream event arriving at integer tick TS.
type Event struct {
	TS  int64
	Key string
}

type waiter struct {
	admit int64 // earliest feasible admission tick
	post  int64 // bucket balance right after being served at admit
}

// Limiter is a FIFO backpressure limiter over a token bucket.
type Limiter struct {
	mu sync.Mutex

	bucket    *tb.Bucket
	cap, rate int64

	lastTS int64
	seen   bool
	epoch  int64 // arrival tick of the current backlog's first waiter

	q    []waiter // waiting events in FIFO order
	head int      // head pointer: O(1) dequeue without rescanning

	probe   int // entries inspected by latest refill-driven batch; tests only
	dropped int // always zero: backpressure never drops
}

func New(B, r int) (*Limiter, error) {
	b, err := tb.New(B, r)
	if err != nil {
		return nil, err
	}
	return &Limiter{bucket: b, cap: int64(B), rate: int64(r)}, nil
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// Feed processes events in input order and returns each admission tick. The
// batch is validated wholesale first, so a rejected batch leaves no trace.
func (l *Limiter) Feed(events []Event) ([]int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prevTS, havePrev := l.lastTS, l.seen
	for _, e := range events {
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
		if havePrev && e.TS < prevTS {
			return nil, ErrTSRollback
		}
		prevTS, havePrev = e.TS, true
	}
	out := make([]int64, len(events))
	for i, e := range events {
		dt := int64(0)
		if l.seen {
			dt = e.TS - l.lastTS
		}
		l.seen, l.lastTS = true, e.TS
		out[i] = l.ingest(e.TS, dt)
	}
	return out, nil
}

// reconcile serves head waiters due by ts (head pointer only) and corrects
// the bucket balance to the post-service balance at ts.
func (l *Limiter) reconcile(ts int64) {
	l.probe = 0
	for l.head < len(l.q) {
		l.probe++
		if l.q[l.head].admit > ts {
			break
		}
		l.head++
	}
	a, s := l.epoch, int64(0)
	if l.head > 0 {
		w := l.q[l.head-1]
		a, s = w.admit, w.post
	}
	if l.head == len(l.q) {
		l.q, l.head = l.q[:0], 0
	}
	l.bucket.Adjust(min(l.cap, s+l.rate*(ts-a)) - int64(l.bucket.Tokens()))
}

func (l *Limiter) ingest(ts, dt int64) int64 {
	l.bucket.Refill(dt)
	if l.head < len(l.q) {
		l.reconcile(ts)
	} else {
		l.probe = 0
	}
	if l.bucket.Take() {
		return ts
	}
	aPrev, sPrev := ts, int64(0)
	if len(l.q)-l.head > 0 {
		t := l.q[len(l.q)-1]
		aPrev, sPrev = t.admit, t.post
	} else {
		l.epoch = ts
	}
	if l.rate == 0 {
		l.q = append(l.q, waiter{admit: Never})
		return Never
	}
	admit := aPrev
	if d := int64(1) - sPrev; d > 0 {
		admit = aPrev + ceilDiv(d, l.rate)
	}
	if ts > admit {
		admit = ts
	}
	post := min(l.cap, sPrev+l.rate*(admit-aPrev)) - 1
	l.q = append(l.q, waiter{admit: admit, post: post})
	return admit
}

// Dropped always returns 0: backpressure makes events wait, never drop.
func (l *Limiter) Dropped() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}
