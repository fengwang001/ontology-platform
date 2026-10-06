// Package cgtest contains an independently written naive per-event
// reference model used for differential testing against cg.Coordinator.
//
// The model is deliberately unsophisticated: groups and volumes are kept in
// plain slices, every membership/confirmation lookup is a linear scan, and
// there are no constant-time shortcuts anywhere. Correctness is the only
// goal; every rule is re-derived from the specification.
package cgtest

import "fmt"

// Kind mirrors cg.Kind's numeric ordering while keeping the model
// self-contained (it intentionally does not import cg).
type Kind int

const (
	OK Kind = iota
	InvalidArgument
	ClockBackward
	NotFound
	Conflict
	StateError
	DuplicateConfirm
	QueueFull
)

// NaiveError is the model's rejection.
type NaiveError struct{ Kind Kind }

func (e *NaiveError) Error() string { return fmt.Sprintf("kind=%d", e.Kind) }

type nVolume struct {
	id  string
	seq uint64
	cap int
	// queue holds write payloads in arrival order.
	queue []string
}

type nSnapshot struct {
	id        uint64
	phase     int // 0 freezing, 1 frozen
	deadline  int64
	point     int64
	confirmed []string
	cutoffs   map[string]uint64
}

type nGroup struct {
	id      string
	members []string
	snap    *nSnapshot
	lastID  uint64
	lastPt  int64
	lastCut map[string]uint64
}

// Naive is the reference model.
type Naive struct {
	defaultCap int
	maxHold    int64
	lastTime   int64
	groups     []*nGroup
	volumes    []*nVolume
	nextSnap   uint64
}

// NewNaive constructs a reference model.
func NewNaive(defaultCap int, maxHold int64) *Naive {
	return &Naive{defaultCap: defaultCap, maxHold: maxHold, nextSnap: 1}
}

func (n *Naive) findGroup(id string) *nGroup {
	for _, g := range n.groups {
		if g.id == id {
			return g
		}
	}
	return nil
}

func (n *Naive) findVolume(id string) *nVolume {
	for _, v := range n.volumes {
		if v.id == id {
			return v
		}
	}
	return nil
}

func groupContains(g *nGroup, vid string) bool {
	for _, m := range g.members {
		if m == vid {
			return true
		}
	}
	return false
}

func confirmedContains(s *nSnapshot, vid string) bool {
	for _, c := range s.confirmed {
		if c == vid {
			return true
		}
	}
	return false
}

func (n *Naive) clock(t int64) *NaiveError {
	if t < n.lastTime {
		return &NaiveError{ClockBackward}
	}
	n.lastTime = t
	return nil
}

// unfreeze ends the snapshot without a record and drains every queue.
func (n *Naive) unfreeze(g *nGroup) {
	g.snap = nil
	for _, vid := range g.members {
		v := n.findVolume(vid)
		for range v.queue {
			v.seq++
		}
		v.queue = v.queue[:0]
	}
}

// timeout performs the mandatory pre-operation timeout detection.
func (n *Naive) timeout(g *nGroup) {
	if g == nil || g.snap == nil {
		return
	}
	s := g.snap
	due := false
	if s.phase == 0 && n.lastTime > s.deadline {
		due = true
	}
	if s.phase == 1 && n.lastTime > s.point+n.maxHold {
		due = true
	}
	if due {
		n.unfreeze(g)
	}
}
