// Package cpt executes changelog compaction: it locates an interval directly
// by site (binary search, never a full scan), folds each Key to its last write,
// emits one compaction record, and keeps a per-site addressability map.
// It depends only on clog.
package cpt

import (
	"sort"

	"ontology/clog"
)

// term is one element of the site-ordered read view: a surviving raw entry
// (rec == nil) or a compaction record (site == rec.Lo).
type term struct {
	site int64
	e    clog.Entry
	rec  *clog.Record
}

// Log is the in-process compacted changelog; use NewLog.
type Log struct {
	nextSeq int64
	raw     []clog.Entry  // surviving raw entries, Seq ascending
	recs    []clog.Record // compaction records, Lo ascending, disjoint
	terms   []term        // merged raw+recs ordered by site
	// siteLo[at] = largest terms index with site <= at, or -1; built for every
	// at in [1, nextSeq) so each site stays addressable.
	siteLo []int
	// skippedOutside counts surviving OUTSIDE-interval entries touched by the
	// most recent Compact while positioning the interval. Direct binary-search
	// positioning touches only the O(1) boundary neighbours. Unexported: it is
	// unreachable through the public API and is read only from package cpt.
	skippedOutside int
}

func NewLog() *Log {
	l := &Log{nextSeq: 1}
	l.rebuild()
	return l
}

func (l *Log) NextSeq() int64 { return l.nextSeq }

// Append adds one raw write and returns its Seq. The new Seq is the largest
// site, so the read view and anchor map extend in O(1); only Compact rebuilds.
func (l *Log) Append(key string, val int) int64 {
	seq := l.nextSeq
	l.nextSeq++
	e := clog.Entry{Seq: seq, Key: key, Val: val}
	l.raw = append(l.raw, e)
	l.terms = append(l.terms, term{site: seq, e: e})
	l.siteLo = append(l.siteLo, len(l.terms)-1)
	return seq
}

// Compact folds the half-open [lo,hi); caller guarantees 1<=lo<hi<=nextSeq.
// Raw entries in [lo,hi) are replaced by one record per Key holding its last
// value; a record already at Lo (same-interval re-compact) re-competes at site
// Lo. Outside entries are never iterated.
func (l *Log) Compact(lo, hi int64) {
	ri := sort.Search(len(l.raw), func(i int) bool { return l.raw[i].Seq >= lo })
	rj := sort.Search(len(l.raw), func(i int) bool { return l.raw[i].Seq >= hi })
	ai := sort.Search(len(l.recs), func(i int) bool { return l.recs[i].Lo >= lo })
	aj := sort.Search(len(l.recs), func(i int) bool { return l.recs[i].Lo >= hi })
	// Only the immediate outside neighbours are inspected to confirm position.
	l.skippedOutside = b2i(ri > 0) + b2i(rj < len(l.raw)) + b2i(ai > 0) + b2i(aj < len(l.recs))

	fold := map[string]int{}
	fsite := map[string]int64{}
	put := func(k string, v int, site int64) { // keep value at the max site
		if s, ok := fsite[k]; !ok || site >= s {
			fsite[k], fold[k] = site, v
		}
	}
	for _, e := range l.raw[ri:rj] { // in-range raw only
		put(e.Key, e.Val, e.Seq)
	}
	for _, r := range l.recs[ai:aj] { // prior record(s) re-compete at Lo
		for k, v := range r.Vals {
			put(k, v, r.Lo)
		}
	}

	kept := make([]clog.Entry, 0, len(l.raw)-(rj-ri))
	kept = append(kept, l.raw[:ri]...)
	kept = append(kept, l.raw[rj:]...)
	l.raw = kept

	nr := make([]clog.Record, 0, len(l.recs)-(aj-ai)+1)
	nr = append(nr, l.recs[:ai]...)
	if len(fold) > 0 { // a Key with no in-range write yields no record entry
		nr = append(nr, clog.Record{Lo: lo, Hi: hi, Vals: fold})
	}
	nr = append(nr, l.recs[aj:]...)
	l.recs = nr
	l.rebuild()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Read returns key's value visible at site at (1 <= at < nextSeq). Starting at
// anchor siteLo[at] and walking back, the first term containing key is the
// visible term with the largest site.
func (l *Log) Read(at int64, key string) (int, bool) {
	for k := l.siteLo[at]; k >= 0; k-- {
		t := &l.terms[k]
		if t.rec == nil {
			if t.e.Key == key {
				return t.e.Val, true
			}
			continue
		}
		if v, ok := t.rec.Vals[key]; ok {
			return v, true
		}
	}
	return 0, false
}

// rebuild merges raw entries and records into the site-ordered view and
// recomputes the anchor map for every addressable site.
func (l *Log) rebuild() {
	l.terms = make([]term, 0, len(l.raw)+len(l.recs))
	i, j := 0, 0
	for i < len(l.raw) || j < len(l.recs) {
		if j == len(l.recs) || (i < len(l.raw) && l.raw[i].Seq < l.recs[j].Lo) {
			l.terms = append(l.terms, term{site: l.raw[i].Seq, e: l.raw[i]})
			i++
		} else {
			l.terms = append(l.terms, term{site: l.recs[j].Lo, rec: &l.recs[j]})
			j++
		}
	}
	l.siteLo = make([]int, l.nextSeq)
	l.siteLo[0] = -1
	k := -1
	for at := int64(1); at < l.nextSeq; at++ {
		for k+1 < len(l.terms) && l.terms[k+1].site <= at {
			k++
		}
		l.siteLo[at] = k
	}
}
