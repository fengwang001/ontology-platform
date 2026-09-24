// Package clog defines the append-only changelog entry shape and the pure
// visibility/interval rules shared by the compaction engine and the API.
// It depends on no other package in this module.
package clog

// Entry is one raw write in the changelog. Seq is system-assigned, starts at
// 1 and is dense (no gaps across appends).
type Entry struct {
	Seq int64
	Key string
	Val int
}

// Record is a compaction record folding every raw write whose Seq falls in
// [Lo, Hi): for each Key only the last value survives. The record behaves as a
// single entry whose Seq is treated as Lo for "last visible entry" comparisons.
type Record struct {
	Lo, Hi int64
	Vals   map[string]int
}

// Visible reports whether a raw entry is visible to a read at site at:
// a raw entry with Seq s is visible for every at >= s.
func (e Entry) Visible(at int64) bool { return at >= e.Seq }

// Visible reports whether a compaction record is visible at site at:
// it is visible from Lo onward (at >= Lo) and invisible for at < Lo.
func (r Record) Visible(at int64) bool { return at >= r.Lo }

// InRange reports whether site s belongs to the half-open interval [lo, hi).
// hi is exclusive, so s == hi is NOT in range.
func InRange(s, lo, hi int64) bool { return s >= lo && s < hi }

// Site returns the Seq at which a compaction record participates in
// "last visible entry" comparisons: always its lower bound Lo.
func (r Record) Site() int64 { return r.Lo }
