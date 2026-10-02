package ontology

import (
	"errors"
	"sync"
)

var ErrInvalidConfig = errors.New("invalid configuration")
var ErrInvalidArgument = errors.New("invalid argument")
var ErrNegativeCount = errors.New("negative count")
var ErrCausalityViolation = errors.New("causality violation")

// Edge describes a unit of work flowing from From to To after Delay.
type Edge struct {
	From  int
	To    int
	Delay int64
}

// EntryDelta is one timestamped counter change in an update batch.
type EntryDelta struct {
	Position int
	Time     int64
	Delta    int64
}

// FrontierChange records one position's externally visible frontier transition.
type FrontierChange struct {
	Position int
	Before   int64
	After    int64
}

type entryKey struct {
	position int
	time     int64
}

type activeSet struct {
	heap    []int64
	present map[int64]bool
}

type Tracker struct {
	mu          sync.RWMutex
	n           int
	dist        [][]int64
	source      []bool
	counts      map[entryKey]int64
	active      []activeSet
	frontier    []int64
	version     int64
	entryVisits int64
}

// Frontier returns the externally encoded frontier: -1 means infinity.
func (tr *Tracker) Frontier(position int) (int64, error) {
	if position < 0 || position >= tr.n {
		return 0, ErrInvalidArgument
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return externalFrontier(tr.frontier[position]), nil
}

// Frontiers returns a versioned, lock-consistent copy of every frontier.
func (tr *Tracker) Frontiers() (int64, []int64) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	frontiers := make([]int64, tr.n)
	for position, value := range tr.frontier {
		frontiers[position] = externalFrontier(value)
	}
	return tr.version, frontiers
}

// Complete reports whether no in-flight work can arrive at or before time.
func (tr *Tracker) Complete(position int, time int64) (bool, error) {
	if position < 0 || position >= tr.n || time < 0 || time > maxTime {
		return false, ErrInvalidArgument
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.frontier[position] == infinity || tr.frontier[position] > time, nil
}
