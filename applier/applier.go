// Package applier manages per-key queues, failure injection, dead letters and the blocked-key set; it depends only on kq.
package applier

import (
	"errors"
	"maps"
	"ontology/kq"
	"slices"
	"sync"
)

type (
	Event = kq.Event
	Kind  int
)

const (
	Applied Kind = iota + 1
	DeadLettered
)

type Decision struct {
	Key  string
	Seq  int64
	Kind Kind
}

var (
	ErrInvalid   = errors.New("invalid argument or event")
	ErrSeqOrder  = errors.New("seq not strictly increasing for key")
	ErrBufferCap = errors.New("total buffered events exceeds maxBuffered")
)

type Applier struct {
	mu                       sync.Mutex
	maxAttempts, maxBuffered int
	fails                    map[Event]int
	queues                   map[string]*kq.Queue
	blocked                  map[string]struct{}
	decisions                []Decision
	dead                     []Event
	bufTotal, lastTickN      int // lastTickN: keys inspected in latest Tick; unexported, absent from the API
}

func New(maxAttempts, maxBuffered int, fails map[Event]int) (*Applier, error) {
	if maxAttempts < 1 || maxBuffered < 0 {
		return nil, ErrInvalid
	}
	fc := make(map[Event]int, len(fails))
	for e, n := range fails {
		if n < 0 || e.Key == "" {
			return nil, ErrInvalid
		}
		fc[e] = n
	}
	return &Applier{maxAttempts: maxAttempts, maxBuffered: maxBuffered, fails: fc, queues: map[string]*kq.Queue{}, blocked: map[string]struct{}{}}, nil
}
func (a *Applier) attempt(e Event, no int) bool { return no > a.fails[e] }

func (a *Applier) Submit(e Event) ([]Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e.Key == "" {
		return nil, ErrInvalid
	}
	q := a.queues[e.Key]
	if q != nil && e.Seq <= q.LastSeq() {
		return nil, ErrSeqOrder
	}
	if q != nil && q.Blocked() && a.bufTotal >= a.maxBuffered {
		return nil, ErrBufferCap
	}
	if q == nil {
		q = kq.New()
		a.queues[e.Key] = q
	}
	q.CommitSeq(e.Seq)
	if !q.Blocked() {
		if a.attempt(e, 1) {
			d := Decision{Key: e.Key, Seq: e.Seq, Kind: Applied}
			a.decisions = append(a.decisions, d)
			return []Decision{d}, nil
		}
		q.Block(e)
		a.blocked[e.Key] = struct{}{}
		return []Decision{}, nil
	}
	q.Buffer(e)
	a.bufTotal++
	return []Decision{}, nil
}

func (a *Applier) Tick() []Decision {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys := slices.Sorted(maps.Keys(a.blocked))
	a.lastTickN = len(keys)
	start := len(a.decisions)
	for _, key := range keys {
		q := a.queues[key]
		q.Retry()
		he, n := q.Head()
		if a.attempt(he, n) {
			a.finalize(key, q, he, Applied)
			a.drainLocked(key, q)
		} else if n >= a.maxAttempts {
			a.finalize(key, q, he, DeadLettered)
			a.dead = append(a.dead, he)
			a.drainLocked(key, q)
		}
	}
	return append([]Decision(nil), a.decisions[start:]...)
}
func (a *Applier) finalize(key string, q *kq.Queue, he Event, kd Kind) {
	q.ClearHead()
	delete(a.blocked, key)
	a.decisions = append(a.decisions, Decision{Key: key, Seq: he.Seq, Kind: kd})
}
func (a *Applier) drainLocked(key string, q *kq.Queue) {
	for {
		e, ok := q.Pop()
		if !ok {
			return
		}
		a.bufTotal--
		if a.attempt(e, 1) {
			a.decisions = append(a.decisions, Decision{Key: key, Seq: e.Seq, Kind: Applied})
			continue
		}
		q.Block(e)
		a.blocked[key] = struct{}{}
		return
	}
}
func (a *Applier) DeadLetters() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Event(nil), a.dead...)
}
func (a *Applier) Applied() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []Event{}
	for _, d := range a.decisions {
		if d.Kind == Applied {
			out = append(out, Event{Key: d.Key, Seq: d.Seq})
		}
	}
	return out
}
