// Package seglog implements the buffered append log: flush to
// segments at B bytes, merge all segments when their count hits T,
// and track logical/physical bytes for the amplification factor.
package seglog

import "ontology/rec"

// Log is the append log. Not goroutine-safe; callers must serialize.
type Log struct {
	B int // flush threshold in bytes
	T int // merge threshold in live segment count

	buf      []rec.Rec
	bufBytes int
	segs     [][]rec.Rec

	seq      int64
	logical  int64
	physical int64

	liveSegs int // live segment count after the most recent Append
}

// New creates a Log with flush threshold b and merge threshold t.
func New(b, t int) *Log {
	return &Log{B: b, T: t}
}

// Append adds one record and applies the flush/merge rules.
func (l *Log) Append(k, v string) {
	l.seq++
	l.buf = append(l.buf, rec.Rec{Key: k, Val: v, Seq: l.seq})
	n := len(k) + len(v)
	l.bufBytes += n
	l.logical += int64(n)

	if l.bufBytes >= l.B {
		l.flush()
	}
	if len(l.segs) >= l.T {
		l.merge()
	}
	l.liveSegs = len(l.segs)
}

// flush writes the whole buffer out as one new segment.
func (l *Log) flush() {
	seg := l.buf
	l.buf = nil
	l.bufBytes = 0
	l.physical += rec.SumBytes(seg)
	l.segs = append(l.segs, seg)
}

// merge combines all live segments into one, keeping only the
// latest (max seq) record per key.
func (l *Log) merge() {
	var all []rec.Rec
	for _, s := range l.segs {
		all = append(all, s...)
	}
	merged := rec.MergeLatest(all)
	l.physical += rec.SumBytes(merged)
	l.segs = [][]rec.Rec{merged}
}

// Get returns the newest value for k: buffer first (newest to
// oldest), then segments (newest to oldest).
func (l *Log) Get(k string) (string, bool) {
	for i := len(l.buf) - 1; i >= 0; i-- {
		if l.buf[i].Key == k {
			return l.buf[i].Val, true
		}
	}
	for i := len(l.segs) - 1; i >= 0; i-- {
		for j := len(l.segs[i]) - 1; j >= 0; j-- {
			if l.segs[i][j].Key == k {
				return l.segs[i][j].Val, true
			}
		}
	}
	return "", false
}

// LogicalBytes is the total logical bytes ever appended.
func (l *Log) LogicalBytes() int64 { return l.logical }

// PhysicalBytes is the total bytes written by flushes and merges.
func (l *Log) PhysicalBytes() int64 { return l.physical }
