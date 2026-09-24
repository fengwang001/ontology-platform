// Package applier manages per-key queues, failure injection and dead letters.
package applier

import (
	"errors"
	"sort"
	"sync"

	"ontology/kq"
)

// The three rejection classes are distinct sentinels.
var (
	ErrInvalidArgument  = errors.New("cdc: invalid argument or event")
	ErrSeqNotIncreasing = errors.New("cdc: seq must be strictly increasing per key")
	ErrBufferFull       = errors.New("cdc: total buffered events at maxBuffered")
)

type Kind int

const (
	Applied Kind = iota
	DeadLettered
)

// Record is one finalization, in occurrence order.
type Record struct {
	Key  string
	Seq  int64
	Kind Kind
}

// Applier is safe for concurrent use.
type Applier struct {
	mu          sync.Mutex
	maxAttempts int
	maxBuffered int
	fails       map[kq.Event]int
	keys        map[string]*kq.Queue
	blocked     map[string]struct{}
	bufTotal    int
	applied     []kq.Event
	dead        []kq.Event
	tickChecked int // keys inspected last Tick: blocked set at Tick start only
}

func New(maxAttempts, maxBuffered int, fails map[kq.Event]int) *Applier {
	return &Applier{maxAttempts: maxAttempts, maxBuffered: maxBuffered, fails: fails,
		keys: map[string]*kq.Queue{}, blocked: map[string]struct{}{}}
}

func (a *Applier) failsOn(e kq.Event, attempt int) bool { return attempt <= a.fails[e] }

// Submit applies e immediately when its key is free, otherwise buffers it.
// All validation precedes any state change, so a rejection is atomic.
func (a *Applier) Submit(e kq.Event) ([]Record, error) {
	if e.Key == "" {
		return nil, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.keys[e.Key]
	if q == nil {
		q = &kq.Queue{}
	}
	if !q.Accepts(e.Seq) {
		return nil, ErrSeqNotIncreasing
	}
	if q.Blocked() && a.bufTotal >= a.maxBuffered {
		return nil, ErrBufferFull
	}
	if a.keys[e.Key] == nil {
		a.keys[e.Key] = q
	}
	q.NoteSubmitted(e.Seq)
	if q.Blocked() {
		q.Append(e)
		a.bufTotal++
		return nil, nil
	}
	if a.failsOn(e, 1) {
		q.Block(e)
		a.blocked[e.Key] = struct{}{}
		return nil, nil
	}
	a.applied = append(a.applied, e)
	return []Record{{Key: e.Key, Seq: e.Seq, Kind: Applied}}, nil
}

// drain empties q's FIFO after its head finalized; key was just unblocked.
func (a *Applier) drain(key string, q *kq.Queue, out []Record) []Record {
	for {
		e, ok := q.PopFront()
		if !ok {
			return out
		}
		a.bufTotal--
		if a.failsOn(e, 1) {
			q.Block(e)
			a.blocked[key] = struct{}{}
			return out
		}
		a.applied = append(a.applied, e)
		out = append(out, Record{Key: e.Key, Seq: e.Seq, Kind: Applied})
	}
}

// Tick retries once each key blocked at Tick start, in key byte order.
func (a *Applier) Tick() []Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := make([]string, 0, len(a.blocked))
	for key := range a.blocked {
		snap = append(snap, key)
	}
	sort.Strings(snap)
	a.tickChecked = len(snap) // invariant: proportional to blocked keys only
	var out []Record
	for _, key := range snap {
		q := a.keys[key]
		head, tries := q.Head()
		if tries >= a.maxAttempts { // budget already exhausted by earlier failures
			a.dead = append(a.dead, head)
			out = append(out, Record{Key: key, Seq: head.Seq, Kind: DeadLettered})
		} else if attempt := q.Retry(); !a.failsOn(head, attempt) {
			a.applied = append(a.applied, head)
			out = append(out, Record{Key: key, Seq: head.Seq, Kind: Applied})
		} else if attempt < a.maxAttempts {
			continue // stay blocked; this key is done for this tick
		} else {
			a.dead = append(a.dead, head)
			out = append(out, Record{Key: key, Seq: head.Seq, Kind: DeadLettered})
		}
		q.Unblock()
		delete(a.blocked, key)
		out = a.drain(key, q, out)
	}
	return out
}

func (a *Applier) Applied() []kq.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]kq.Event(nil), a.applied...)
}
func (a *Applier) DeadLetters() []kq.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]kq.Event(nil), a.dead...)
}
