// Package hotcount provides an approximate, concurrency-safe hot-element
// counter built from a multi-row counting sketch plus a bounded candidate
// list.
//
// Each Add(element, count) arrival adds count into one hashed cell per row.
// Estimate(element) is the minimum over the element's cells, so it never
// under-estimates the true accumulated weight. The candidate list keeps at
// most Config.MaxCandidates elements in a total order (estimate descending,
// element ascending) and is maintained incrementally: an element already in
// the list is refreshed on its own arrival; otherwise it is inserted while
// the list is not full, or replaces the worst element when it ranks higher.
// Candidates are not refreshed by other elements' arrivals, so a stored
// estimate may lag the live estimate after collisions.
//
// All methods are safe for concurrent use.
package hotcount
