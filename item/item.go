// Package item defines the priority-queue element type and re-exports the
// heap sentinel errors so callers depend on a single domain package.
package item

import "ontology/heap"

// Item is a query-plan work item keyed by an estimated cost.
type Item struct {
	Name string
	Cost int
}

// Less orders items by ascending cost.
func Less(a, b Item) bool { return a.Cost < b.Cost }

var (
	ErrGone       = heap.ErrGone
	ErrUnknown    = heap.ErrUnknown
	ErrNotSmaller = heap.ErrNotSmaller
)
