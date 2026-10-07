package reflog

import "sort"

// refLog is the log of one reference. Records are kept in two
// time-ordered slices, one per retention tier, so that expiry is a
// prefix cut located by binary search: its cost never grows with the
// number of unexpired records.
type refLog struct {
	hasCurrent  bool
	current     CommitID
	nextSeq     uint64
	reachable   []*Record
	unreachable []*Record
}

// reachableTier reports whether rec falls in the reachable tier:
// its old value is an ancestor of (or equal to) the head whose
// ancestor set is ancestors. Records with an empty old value
// (creations) are never in the reachable tier.
func reachableTier(rec *Record, ancestors map[CommitID]bool) bool {
	return rec.Old != "" && ancestors[rec.Old]
}

// recordLess orders records by (Time, Seq).
func recordLess(a, b *Record) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	return a.Seq < b.Seq
}

// appendRecord appends a new record, placing it in its tier. Times
// are non-decreasing per reference, so each tier slice stays sorted.
func (l *refLog) appendRecord(old, new CommitID, now int64, who string, ancestors map[CommitID]bool) {
	rec := &Record{Seq: l.nextSeq, Old: old, New: new, Time: now, Who: who}
	l.nextSeq++
	if reachableTier(rec, ancestors) {
		l.reachable = append(l.reachable, rec)
	} else {
		l.unreachable = append(l.unreachable, rec)
	}
}

// merged returns all records ordered by (Time, Seq).
func (l *refLog) merged() []*Record {
	out := make([]*Record, 0, len(l.reachable)+len(l.unreachable))
	i, j := 0, 0
	for i < len(l.reachable) && j < len(l.unreachable) {
		if recordLess(l.reachable[i], l.unreachable[j]) {
			out = append(out, l.reachable[i])
			i++
		} else {
			out = append(out, l.unreachable[j])
			j++
		}
	}
	out = append(out, l.reachable[i:]...)
	out = append(out, l.unreachable[j:]...)
	return out
}

// reclassify re-partitions every record against the ancestor set of
// the reference's new head. Relative order inside each tier is
// preserved, so both slices remain time-sorted.
func (l *refLog) reclassify(ancestors map[CommitID]bool) {
	var reach, unreach []*Record
	for _, rec := range l.merged() {
		if reachableTier(rec, ancestors) {
			reach = append(reach, rec)
		} else {
			unreach = append(unreach, rec)
		}
	}
	l.reachable, l.unreachable = reach, unreach
}

// markAllUnreachable moves every record to the unreachable tier; it
// is used when the reference is deleted.
func (l *refLog) markAllUnreachable() {
	l.unreachable = l.merged()
	l.reachable = nil
}

// cutTier drops the expired prefix of a time-sorted tier slice. A
// record expires when now-rec.Time >= retention (equality counts).
// Returns the kept suffix and the number of expired records.
func cutTier(recs []*Record, now, retention int64) ([]*Record, int) {
	kept := sort.Search(len(recs), func(i int) bool {
		return now-recs[i].Time < retention
	})
	for i := 0; i < kept; i++ {
		recs[i] = nil
	}
	return recs[kept:], kept
}

// expire physically removes expired records from both tiers.
func (l *refLog) expire(now int64, cfg Config) int {
	var n int
	l.reachable, n = cutTier(l.reachable, now, cfg.ReachableRetention)
	var m int
	l.unreachable, m = cutTier(l.unreachable, now, cfg.UnreachableRetention)
	return n + m
}

// forEachUnexpired calls fn for every record whose age is below its
// tier retention, without touching the expired prefixes.
func (l *refLog) forEachUnexpired(now int64, cfg Config, fn func(*Record)) {
	i := sort.Search(len(l.reachable), func(i int) bool {
		return now-l.reachable[i].Time < cfg.ReachableRetention
	})
	for _, rec := range l.reachable[i:] {
		fn(rec)
	}
	j := sort.Search(len(l.unreachable), func(j int) bool {
		return now-l.unreachable[j].Time < cfg.UnreachableRetention
	})
	for _, rec := range l.unreachable[j:] {
		fn(rec)
	}
}
