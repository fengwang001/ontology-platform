// Package compactlog implements an append-only log with key compaction,
// tombstone retention, and multi-consumer catch-up reads.
package compactlog

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Distinguishable rejection reasons.
var (
	ErrEmptyKey        = errors.New("compactlog: empty key")
	ErrTimeRegression  = errors.New("compactlog: time regression")
	ErrLogFull         = errors.New("compactlog: log is full")
	ErrUnknownConsumer = errors.New("compactlog: unknown consumer")
	ErrConsumerExists  = errors.New("compactlog: consumer already exists")
)

// Record is a single log entry. Sequences are assigned in append order and
// never renumbered; compaction only leaves holes in the sequence.
type Record struct {
	Seq       uint64
	Key       string
	Value     string
	Tombstone bool
	WrittenAt time.Time
}

// consumer tracks a subscriber's read position and materialized view.
type consumer struct {
	nextSeq uint64
	view    map[string]string
}

// Log is a concurrency-safe compacted log.
type Log struct {
	mu        sync.Mutex
	capacity  int
	retention time.Duration
	nextSeq   uint64
	lastWrite time.Time
	hasWrite  bool
	records   map[uint64]Record
	consumers map[string]*consumer
}

// New creates a log. capacity is the max number of records the log may hold;
// retention is how long delete tombstones are kept.
func New(capacity int, retention time.Duration) *Log {
	return &Log{
		capacity:  capacity,
		retention: retention,
		nextSeq:   1,
		records:   make(map[uint64]Record),
		consumers: make(map[string]*consumer),
	}
}

// Append appends a record. tombstone=true marks a delete (value ignored).
// ts is the write time and must not be earlier than the last accepted write.
// A rejected append changes neither clock, sequence counter, nor log content.
func (l *Log) Append(key, value string, tombstone bool, ts time.Time) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hasWrite && ts.Before(l.lastWrite) {
		return 0, ErrTimeRegression
	}
	if len(l.records) >= l.capacity {
		return 0, ErrLogFull
	}
	seq := l.nextSeq
	if tombstone {
		value = ""
	}
	l.records[seq] = Record{Seq: seq, Key: key, Value: value, Tombstone: tombstone, WrittenAt: ts}
	l.nextSeq++
	l.lastWrite = ts
	l.hasWrite = true
	return seq, nil
}

// Subscribe registers a consumer that catches up from the log start.
func (l *Log) Subscribe(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.consumers[name]; ok {
		return ErrConsumerExists
	}
	l.consumers[name] = &consumer{nextSeq: 1, view: make(map[string]string)}
	return nil
}

// ReadNext returns the consumer's next record in sequence order (skipping
// holes) and applies it to the consumer's view. ok=false when caught up.
func (l *Log) ReadNext(name string) (rec Record, ok bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.consumers[name]
	if !ok {
		return Record{}, false, ErrUnknownConsumer
	}
	for c.nextSeq < l.nextSeq {
		seq := c.nextSeq
		c.nextSeq++
		rec, live := l.records[seq]
		if !live {
			continue // compaction hole
		}
		if rec.Tombstone {
			delete(c.view, rec.Key)
		} else {
			c.view[rec.Key] = rec.Value
		}
		return rec, true, nil
	}
	return Record{}, false, nil
}

// View returns a copy of the consumer's materialized view.
func (l *Log) View(name string) (map[string]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.consumers[name]
	if !ok {
		return nil, ErrUnknownConsumer
	}
	out := make(map[string]string, len(c.view))
	for k, v := range c.view {
		out[k] = v
	}
	return out, nil
}

// Compact compacts the log as of now: keep only the latest record per key
// and drop tombstones whose write time is older than the retention period.
func (l *Log) Compact(now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hasWrite && now.Before(l.lastWrite) {
		return ErrTimeRegression
	}
	latest := make(map[string]uint64, len(l.records))
	for seq, rec := range l.records {
		if cur, ok := latest[rec.Key]; !ok || seq > cur {
			latest[rec.Key] = seq
		}
	}
	for seq, rec := range l.records {
		covered := seq != latest[rec.Key]
		expiredTombstone := rec.Tombstone && now.Sub(rec.WrittenAt) > l.retention
		if covered || expiredTombstone {
			delete(l.records, seq)
		}
	}
	return nil
}

// Snapshot returns all live records in sequence order (for tests/debug).
func (l *Log) Snapshot() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotLocked()
}

func (l *Log) snapshotLocked() []Record {
	seqs := make([]uint64, 0, len(l.records))
	for seq := range l.records {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	out := make([]Record, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, l.records[seq])
	}
	return out
}

// String formats a record for test logging.
func (r Record) String() string {
	kind := "put"
	if r.Tombstone {
		kind = "tombstone"
	}
	return fmt.Sprintf("#%d %s key=%q value=%q @%s", r.Seq, kind, r.Key, r.Value, r.WrittenAt.Format(time.RFC3339Nano))
}
