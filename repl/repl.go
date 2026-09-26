// Package repl holds the leader's Raft replication bookkeeping:
// term, per-follower matchIndex/nextIndex and the cluster commitIndex.
// It depends only on package log.
package repl

import (
	"errors"
	"sort"
	"sync"

	"ontology/log"
)

// Distinct, decidable sentinel errors for every rejected operation.
var (
	// ErrFollowerIndex: f targets the leader (1) or is outside [2, n].
	ErrFollowerIndex = errors.New("repl: follower index out of range")
	// ErrTermMismatch: an Append used a term other than the current term.
	ErrTermMismatch = errors.New("repl: append term is not the current term")
	// ErrStaleTerm: an Elect used a term not greater than the current term.
	ErrStaleTerm = errors.New("repl: election term must be greater than current term")
	// ErrHintRange: a Replicate reported r outside [0, Len()].
	ErrHintRange = errors.New("repl: replicated index r out of range")
)

// Leader is node 1's bookkeeping state. Node ids are 1..n; followers are 2..n.
type Leader struct {
	mu     sync.RWMutex
	lg     *log.Log
	n      int
	quorum int
	term   int
	// Slices are indexed by node id; index 0 and 1 stay zero for match.
	match  []int
	next   []int
	commit int
	// reads counts log term lookups performed by the most recent CommitIndex
	// computation. Unexported: never exposed through any method.
	reads int
}

// New creates a cluster of n (odd, n>=1) nodes at term 0; call Elect to take
// office in a real term. Followers start matchIndex=0, nextIndex=1.
func New(n int) *Leader {
	l := &Leader{
		lg:     log.New(),
		n:      n,
		quorum: n/2 + 1,
		term:   0,
		match:  make([]int, n+1),
		next:   make([]int, n+1),
	}
	for f := 1; f <= n; f++ {
		l.next[f] = 1
	}
	return l
}

// Term returns the current leader term.
func (l *Leader) Term() int { l.mu.RLock(); defer l.mu.RUnlock(); return l.term }

// Len returns the leader log length (real entries, excluding the sentinel).
func (l *Leader) Len() int { return l.lg.Len() }

// Append appends an entry of the given term. The term must equal the current
// term, otherwise ErrTermMismatch is returned and nothing changes.
func (l *Leader) Append(term int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if term != l.term {
		return ErrTermMismatch
	}
	l.lg.Append(term)
	return nil
}

// Replicate records one follower response. On ok, the follower's log matches
// the leader's first r entries: matchIndex[f]=r, nextIndex[f]=r+1. On reject,
// matchIndex[f] is untouched and nextIndex[f]=r+1 (r is the follower's hint).
func (l *Leader) Replicate(f int, ok bool, r int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f < 2 || f > l.n { // node 1 is the leader; followers are 2..n
		return ErrFollowerIndex
	}
	if r < 0 || r > l.lg.Len() {
		return ErrHintRange
	}
	if ok {
		l.match[f] = r
	}
	l.next[f] = r + 1
	return nil
}

// Elect installs the leader in term t, requiring t > current term. Match
// indices reset to 0 and next indices to Len()+1; commitIndex is untouched.
func (l *Leader) Elect(t int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t <= l.term {
		return ErrStaleTerm
	}
	l.term = t
	for f := 2; f <= l.n; f++ {
		l.match[f] = 0
		l.next[f] = l.lg.Len() + 1
	}
	return nil
}

// MatchIndex returns the known replicated prefix length of follower f.
func (l *Leader) MatchIndex(f int) int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.match[f]
}

// NextIndex returns the next index to send to follower f.
func (l *Leader) NextIndex(f int) int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.next[f]
}

// CommitIndex advances (monotonically) to the quorum-th largest replicated
// prefix index (leader included) when that entry is of the current term, then
// returns it. It performs one term lookup at most, never a full scan.
func (l *Leader) CommitIndex() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reads = 0
	vals := make([]int, 0, l.n)
	vals = append(vals, l.lg.Len()) // the leader always has its own log
	for f := 2; f <= l.n; f++ {
		vals = append(vals, l.match[f])
	}
	sort.Ints(vals)
	c := vals[l.n-l.quorum] // quorum-th largest value
	if c > l.commit {
		l.reads++
		if l.lg.Term(c) == l.term {
			l.commit = c
		}
	}
	return l.commit
}
