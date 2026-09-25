// Package ra holds per-process state for Ricart–Agrawala mutual
// exclusion and the OK/defer decision on an incoming REQUEST.
package ra

import (
	"errors"
	"sync"

	"ontology/ord"
)

// Sentinel errors: rejected operations fail atomically, state untouched.
var (
	ErrPIDOutOfRange    = errors.New("ra: pid out of range")
	ErrDuplicateRequest = errors.New("ra: process already interested")
	ErrNotInterested    = errors.New("ra: process has no pending request")
	ErrNegativeTS       = errors.New("ra: negative timestamp")
)

const (
	Grant Verdict = iota // reply OK immediately
	Defer                // withhold OK until the receiver leaves
)

type Verdict int

// Judge: a disinterested receiver always grants; otherwise the smaller
// (ts,pid) ticket wins, so a smaller sender is granted through.
func Judge(receiverInterested bool, receiver, sender ord.Ticket) Verdict {
	if !receiverInterested || ord.Less(sender, receiver) {
		return Grant
	}
	return Defer
}

type proc struct {
	interested bool
	ts         int
	deferred   map[int]struct{} // pids whose OK this process withholds
	okCount    int              // OKs received for the current request
}

// System is n processes with all state in memory.
type System struct {
	mu        sync.Mutex
	n         int
	procs     []proc
	granted   [][]uint64 // granted[from] is a bitset of pids holding its OK
	lastCheck int        // entries inspected by last collection decision; unexported
}

func NewSystem(n int) *System {
	s := &System{n: n, granted: make([][]uint64, n), procs: make([]proc, n)}
	for i := range s.procs {
		s.procs[i].deferred = map[int]struct{}{}
		s.granted[i] = make([]uint64, (n+63)>>6)
	}
	return s
}

func (s *System) Request(pid, ts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pid < 0 || pid >= s.n {
		return ErrPIDOutOfRange
	}
	if ts < 0 {
		return ErrNegativeTS
	}
	if s.procs[pid].interested {
		return ErrDuplicateRequest
	}
	p := &s.procs[pid]
	p.interested, p.ts, p.okCount = true, ts, 0
	p.deferred = map[int]struct{}{}
	return nil
}

// Resolve settles OK/defer for every pair; idempotent via s.granted.
func (s *System) Resolve() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for x := 0; x < s.n; x++ {
		if !s.procs[x].interested {
			continue
		}
		for y := 0; y < s.n; y++ {
			if y == x || s.granted[y][x>>6]&(1<<(uint(x)&63)) != 0 {
				continue
			}
			grant := !s.procs[y].interested
			if !grant {
				ry := ord.Ticket{TS: s.procs[y].ts, PID: y}
				rx := ord.Ticket{TS: s.procs[x].ts, PID: x}
				grant = Judge(true, ry, rx) == Grant
			}
			if grant {
				s.granted[y][x>>6] |= 1 << (uint(x) & 63)
				s.procs[x].okCount++
			} else {
				s.procs[y].deferred[x] = struct{}{}
			}
		}
		s.lastCheck = 1 // O(1) collection decision: one counter comparison, no scan
	}
}

func (s *System) Enterable(pid int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pid < 0 || pid >= s.n {
		return false, ErrPIDOutOfRange
	}
	p := &s.procs[pid]
	return p.interested && p.okCount == s.n-1, nil
}

// Leave releases every process pid deferred, then ends pid's request.
func (s *System) Leave(pid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pid < 0 || pid >= s.n {
		return ErrPIDOutOfRange
	}
	p := &s.procs[pid]
	if !p.interested {
		return ErrNotInterested
	}
	for y := range p.deferred {
		s.granted[pid][y>>6] |= 1 << (uint(y) & 63)
		s.procs[y].okCount++
	}
	p.deferred = map[int]struct{}{}
	for q := 0; q < s.n; q++ {
		delete(s.procs[q].deferred, pid)
		s.granted[q][pid>>6] &^= 1 << (uint(pid) & 63) // OKs were for this ended request
	}
	p.interested = false
	return nil
}
