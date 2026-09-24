// Package doc is the document state over the rga tree: validated
// Insert/Delete, visible Text, and an internal lookup-cost probe. It
// depends only on rga.
package doc

import (
	"errors"
	"sync"

	"ontology/rga"
)

// Sentinel errors: every rejected operation has a distinct, decidable cause.
var (
	ErrInvalidID      = errors.New("rga: invalid element id (empty replica)")
	ErrDuplicateID    = errors.New("rga: duplicate element id")
	ErrPrevNotFound   = errors.New("rga: predecessor element does not exist")
	ErrDeleteNotFound = errors.New("rga: delete target does not exist")
	ErrAlreadyDeleted = errors.New("rga: element already deleted")
)

// Doc is a process-memory replicated document. It is safe for concurrent
// use: Text takes a read lock, mutations take the write lock.
type Doc struct {
	mu sync.RWMutex
	t  *rga.Tree

	// lastProbe counts elements examined while locating prev in the most
	// recent Insert. Unexported: never reachable through the public API.
	lastProbe int
}

// New returns an empty document.
func New() *Doc {
	return &Doc{t: rga.New()}
}

// Insert adds ch under id right after prev (zero ID means the head). Every
// precondition is checked before any state is touched, so a rejected call
// leaves the document unchanged.
func (d *Doc) Insert(prev, id rga.ID, ch rune) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id.Replica == "" {
		return ErrInvalidID
	}
	if d.t.Has(id) {
		return ErrDuplicateID
	}
	if prev != (rga.ID{}) && !d.t.Has(prev) {
		return ErrPrevNotFound
	}
	d.lastProbe = d.t.Insert(prev, id, ch)
	return nil
}

// Delete tombstones id: the node is kept and stays a valid anchor.
func (d *Doc) Delete(id rga.ID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.t.Has(id) {
		return ErrDeleteNotFound
	}
	if d.t.Deleted(id) {
		return ErrAlreadyDeleted
	}
	d.t.Delete(id)
	return nil
}

// Text returns the current visible text.
func (d *Doc) Text() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.t.Text()
}

// ProbeIsConstant verifies across several document sizes that the
// predecessor-lookup probe stays within a small constant (hash lookup
// rather than a scan). It returns only the verdict; the probe value itself
// is never exposed through any exported API.
func ProbeIsConstant() bool {
	for _, m := range []int{100, 1000, 10000} {
		d := New()
		var prev rga.ID
		for i := 1; i <= m; i++ {
			id := rga.ID{Lamport: i, Replica: "A"}
			if err := d.Insert(prev, id, 'a'); err != nil {
				return false
			}
			prev = id
		}
		target := rga.ID{Lamport: 1, Replica: "A"}
		if err := d.Insert(target, rga.ID{Lamport: m + 1, Replica: "Z"}, 'z'); err != nil {
			return false
		}
		if d.probe() > 1 {
			return false
		}
	}
	return true
}

// probe returns the last Insert's predecessor-lookup examination count.
// Unexported, white-box tests only.
func (d *Doc) probe() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastProbe
}
