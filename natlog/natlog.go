// Package natlog records block allocation and release events and answers
// retrospective lookups of the subscriber holding a port at a given time.
package natlog

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidParam reports a lookup parameter outside its allowed range,
	// including a time beyond the largest accepted operation time.
	ErrInvalidParam = errors.New("natlog: invalid parameter")
	// ErrNotAllocated reports that no allocation covers the query.
	ErrNotAllocated = errors.New("natlog: address and port not allocated")
)

// Kind is the log entry kind.
type Kind int

const (
	// Alloc marks a block allocation to a subscriber.
	Alloc Kind = iota
	// Free marks a block release.
	Free
)

func (k Kind) String() string {
	if k == Free {
		return "FREE"
	}
	return "ALLOC"
}

// Entry is one log record. Seq starts at 1 and Time is non-decreasing.
type Entry struct {
	Seq   int
	Kind  Kind
	Sub   int
	Addr  int
	First int
	Last  int
	Time  int64
}

const maxNow = 1_000_000_000_000

// Log is an append-only allocation log with per-block history indexes.
type Log struct {
	mu       sync.Mutex
	a, l, h  int
	s        int
	entries  []Entry
	byBlock  map[[2]int][]int
	maxSeen  int64
	lastCmps int
}

// NewLog builds an empty log for a pool with the given geometry.
func NewLog(A, L, H, S int) *Log {
	return &Log{a: A, l: L, h: H, s: S, byBlock: make(map[[2]int][]int), maxSeen: -1}
}

// Observe records the largest accepted operation time.
func (l *Log) Observe(now int64) {
	l.mu.Lock()
	if now > l.maxSeen {
		l.maxSeen = now
	}
	l.mu.Unlock()
}

// Append adds an entry and returns it with its assigned sequence number.
func (l *Log) Append(kind Kind, sub, addr, first, last int, tm int64) Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Entry{Seq: len(l.entries) + 1, Kind: kind, Sub: sub, Addr: addr, First: first, Last: last, Time: tm}
	l.entries = append(l.entries, e)
	key := [2]int{addr, first}
	l.byBlock[key] = append(l.byBlock[key], len(l.entries)-1)
	return e
}

// Entries returns a copy of all entries in sequence order.
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Lookup returns the subscriber holding the block covering (addr, port) at
// time t: the allocation with ALLOC <= t < FREE. It never advances the clock.
func (l *Log) Lookup(addr, port int, t int64) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastCmps = 0
	if addr < 0 || addr >= l.a || port < l.l || port > l.h || t < 0 || t > maxNow || t > l.maxSeen {
		return 0, ErrInvalidParam
	}
	first := l.l + ((port-l.l)/l.s)*l.s
	hist := l.byBlock[[2]int{addr, first}]
	// Entries of one block alternate ALLOC, FREE, ALLOC, ... so the
	// allocations sit at even positions with non-decreasing times.
	n := (len(hist) + 1) / 2
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		l.lastCmps++
		if l.entries[hist[2*mid]].Time <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0, ErrNotAllocated
	}
	alloc := l.entries[hist[2*(lo-1)]]
	l.lastCmps++
	if fi := 2*(lo-1) + 1; fi < len(hist) && l.entries[hist[fi]].Time <= t {
		return 0, ErrNotAllocated
	}
	return alloc.Sub, nil
}
