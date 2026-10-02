package ontology

import (
	"errors"
	"sync"
)

var ErrInvalidConfig = errors.New("ontology: invalid tracker configuration")
var ErrInvalidArgument = errors.New("ontology: invalid argument")
var ErrNegativeCount = errors.New("ontology: negative count")
var ErrCausalViolation = errors.New("ontology: causal violation")

type Edge struct {
	From  int
	To    int
	Delay int64
}

type Delta struct {
	Position int
	Time     int64
	Amount   int64
}

type FrontierChange struct {
	Position int
	Before   int64
	After    int64
}

type Rejection struct {
	Reason   error
	Position int
	Time     int64
}

const infinity int64 = -1

type ledgerKey struct {
	position int
	time     int64
}

type Tracker struct {
	mu        sync.RWMutex
	n         int
	distance  [][]int64
	sources   map[int]struct{}
	version   uint64
	ledger    map[ledgerKey]uint64
	frontiers []int64
	active    *activeIndex
	entryHits uint64
}

func NewTracker(n int, edges []Edge, sources []int) (*Tracker, error) {
	if n < 1 || n > 64 || len(edges) > 512 || len(sources) == 0 {
		return nil, ErrInvalidConfig
	}

	seenSources := make(map[int]struct{}, len(sources))
	for _, source := range sources {
		if source < 0 || source >= n {
			return nil, ErrInvalidConfig
		}
		if _, duplicated := seenSources[source]; duplicated {
			return nil, ErrInvalidConfig
		}
		seenSources[source] = struct{}{}
	}

	oneWay := make([][]int64, n)
	for i := range oneWay {
		oneWay[i] = make([]int64, n)
		for j := range oneWay[i] {
			oneWay[i][j] = -1
		}
	}

	for _, edge := range edges {
		if edge.From < 0 || edge.From >= n || edge.To < 0 || edge.To >= n ||
			edge.Delay < 0 || edge.Delay > 1_000_000 {
			return nil, ErrInvalidConfig
		}
		current := oneWay[edge.From][edge.To]
		if current == -1 || edge.Delay < current {
			oneWay[edge.From][edge.To] = edge.Delay
		}
	}

	for middle := 0; middle < n; middle++ {
		for from := 0; from < n; from++ {
			throughMiddle := oneWay[from][middle]
			if throughMiddle == -1 {
				continue
			}
			for to := 0; to < n; to++ {
				rest := oneWay[middle][to]
				if rest == -1 {
					continue
				}
				candidate := throughMiddle + rest
				current := oneWay[from][to]
				if current == -1 || candidate < current {
					oneWay[from][to] = candidate
				}
			}
		}
	}

	for position := 0; position < n; position++ {
		if oneWay[position][position] == 0 {
			return nil, ErrInvalidConfig
		}
	}

	distance := make([][]int64, n)
	for from := range distance {
		distance[from] = append([]int64(nil), oneWay[from]...)
		distance[from][from] = 0
	}

	return &Tracker{
		n:        n,
		distance: distance,
		sources:  seenSources,
		ledger:   make(map[ledgerKey]uint64),
		frontiers: func() []int64 {
			values := make([]int64, n)
			for i := range values {
				values[i] = infinity
			}
			return values
		}(),
		active: newActiveIndex(n),
	}, nil
}

func (t *Tracker) Frontier(position int) (frontier int64, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if position < 0 || position >= t.n {
		return infinity, ErrInvalidArgument
	}
	return t.frontiers[position], nil
}

func (t *Tracker) Frontiers() (version uint64, frontiers []int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	frontiers = append([]int64(nil), t.frontiers...)
	return t.version, frontiers
}

func (t *Tracker) Complete(position int, time int64) (complete bool, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if position < 0 || position >= t.n || time < 0 || time > 1_000_000_000_000 {
		return false, ErrInvalidArgument
	}
	frontier := t.frontiers[position]
	return frontier == infinity || frontier > time, nil
}

func (t *Tracker) EntryVisits() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.entryHits
}
