// Package gesture converts millisecond button levels into debounced press,
// release, long-press, single-click, and double-click events.
//
// New validates debounce count D, long-press threshold L, and double-click
// window W. Recognizer is safe for concurrent Sample, Run, and Stats calls.
// Every call is serialized internally, so the result is equivalent to one
// valid serial interleaving, and partitioning one level stream between Run
// calls does not change the event table.
package gesture
