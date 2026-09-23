// Package iter iterates a skip list in key order. The iterator is
// fail-fast: any write to the list after creation is reported as
// ErrInvalidated instead of yielding a mixed old/new sequence.
package iter

import (
	"errors"

	"ontology/key"
	"ontology/list"
	"ontology/node"
)

// ErrInvalidated is returned by Next once the list changed after the
// iterator was created.
var ErrInvalidated = errors.New("iter: list modified during iteration")

// Iter walks the bottom level from the front. Not safe for concurrent use.
type Iter struct {
	l       *list.List
	cur     *node.Node // last returned node; nil before the first Next
	version uint64
}

// New creates an iterator over l pinned to l's current version.
func New(l *list.List) *Iter { return &Iter{l: l, version: l.Version()} }

// Next returns the next key. ok is false at the end of the list. Any
// intervening write makes Next fail with ErrInvalidated — including
// deleting the very element the iterator is positioned on.
func (it *Iter) Next() (k key.Key, ok bool, err error) {
	if it.version != it.l.Version() {
		return "", false, ErrInvalidated
	}
	nxt := it.l.Header().Next(0)
	if it.cur != nil {
		nxt = it.cur.Next(0)
	}
	if nxt == nil {
		return "", false, nil
	}
	it.cur = nxt
	return nxt.Key, true, nil
}
