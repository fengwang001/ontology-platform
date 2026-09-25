// Package api is the public face of the hazard-pointer reclaimer. It depends
// only on the retire package (which in turn depends on hazard).
package api

import (
	"errors"

	"ontology/hazard"
	"ontology/retire"
)

// Sentinel errors let callers distinguish every rejection case. All four are
// distinct values.
var (
	ErrInvalidNode    = errors.New("invalid node id")
	ErrAlreadyRetired = errors.New("node already retired")
	ErrNotProtected   = errors.New("thread holds no hazard pointer")
	ErrAlreadyHeld    = errors.New("thread already protects another node")
)

// Reclaimer is the process-memory hazard-pointer reclamation API.
type Reclaimer struct {
	h *hazard.Table
	l *retire.List
}

// New returns an empty reclaimer.
func New() *Reclaimer {
	h := hazard.New()
	return &Reclaimer{h: h, l: retire.New(h)}
}

// Protect publishes nodeID as id's hazard pointer before the node is
// dereferenced. nodeID <= 0, or a thread already protecting another node, is
// rejected with no state change.
func (r *Reclaimer) Protect(id, nodeID int) error {
	if nodeID <= 0 {
		return ErrInvalidNode
	}
	if err := r.h.Set(id, nodeID); err != nil {
		if errors.Is(err, hazard.ErrSlotBusy) {
			return ErrAlreadyHeld
		}
		return err
	}
	return nil
}

// Unprotect clears id's hazard pointer. Clearing an empty slot is rejected
// with no state change.
func (r *Reclaimer) Unprotect(id int) error {
	if err := r.h.Clear(id); err != nil {
		if errors.Is(err, hazard.ErrEmptySlot) {
			return ErrNotProtected
		}
		return err
	}
	return nil
}

// Retire puts nodeID on the retired list. nodeID <= 0 or double retirement is
// rejected with no state change.
func (r *Reclaimer) Retire(nodeID int) error {
	if err := r.l.Retire(nodeID); err != nil {
		if errors.Is(err, retire.ErrInvalidNode) {
			return ErrInvalidNode
		}
		if errors.Is(err, retire.ErrAlreadyRetired) {
			return ErrAlreadyRetired
		}
		return err
	}
	return nil
}

// Reclaim frees retired nodes pointed at by no hazard pointer and returns
// their ids; still-protected nodes wait for a later call.
func (r *Reclaimer) Reclaim() []int { return r.l.Reclaim() }

// State is a point-in-time view used by tests and SelfCheck.
type State struct {
	Hazards map[int]int // thread id -> protected node id
	Retired []int       // retired ids in list order
}

// Snapshot returns a copy of the current hazards and retired list.
func (r *Reclaimer) Snapshot() State {
	return State{Hazards: r.h.Snapshot(), Retired: r.l.Snapshot()}
}

// SelfCheck runs built-in operation sequences verifying the four invariants:
// the canonical six-step scenario, failed-operation atomicity, and eventual
// reclamation. It returns nil when all hold.
func (r *Reclaimer) SelfCheck() error {
	// Canonical scenario (NOTES.md): only 20 frees at step 4, then 10.
	// Run on a fresh instance so calling SelfCheck never mutates the receiver.
	c := New()
	if err := c.Protect(1, 10); err != nil {
		return err
	}
	if err := c.Retire(10); err != nil {
		return err
	}
	if err := c.Retire(20); err != nil {
		return err
	}
	if got := c.Reclaim(); len(got) != 1 || got[0] != 20 {
		return errors.New("selfcheck: first Reclaim must free only 20")
	}
	if err := c.Unprotect(1); err != nil {
		return err
	}
	if got := c.Reclaim(); len(got) != 1 || got[0] != 10 {
		return errors.New("selfcheck: second Reclaim must free 10")
	}
	// Invariant 4: the four decidable rejections occur, are distinct, and
	// leave both the hazards and the retired list exactly as they were.
	q := New()
	q.Retire(9)
	q.Protect(2, 3)
	before := q.Snapshot()
	got := []error{q.Protect(1, 0), q.Retire(0), q.Retire(9), q.Unprotect(7), q.Protect(2, 4)}
	want := []error{ErrInvalidNode, ErrInvalidNode, ErrAlreadyRetired, ErrNotProtected, ErrAlreadyHeld}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			return errors.New("selfcheck: rejection error mismatch")
		}
	}
	after := q.Snapshot()
	same := len(before.Hazards) == len(after.Hazards) && len(before.Retired) == len(after.Retired)
	for k, v := range before.Hazards {
		if after.Hazards[k] != v {
			same = false
		}
	}
	for i := range before.Retired {
		if before.Retired[i] != after.Retired[i] {
			same = false
		}
	}
	if !same {
		return errors.New("selfcheck: rejected operation left a trace")
	}
	return nil
}
