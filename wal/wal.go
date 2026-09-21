// Package wal implements a write-ahead log with an injectable sink.
package wal

import (
	"errors"
	"sync"
)

// Record is a single balance mutation for one shard.
type Record struct {
	Seq   uint64
	Txn   uint64 // transaction id; both records of one transfer share it
	Shard int
	Delta int64
}

// Sink is the durable backend. Callers may inject failures.
type Sink interface {
	Write(p []byte) (int, error)
}

// Log is an append-only write-ahead log. Records live in memory once
// the sink has accepted them; the sink is the durability boundary.
type Log struct {
	mu      sync.Mutex
	sink    Sink
	records []Record
	lastSeq uint64
}

// New creates a Log writing to s. A nil sink discards writes.
func New(s Sink) *Log {
	if s == nil {
		s = discardSink{}
	}
	return &Log{sink: s}
}

type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return len(p), nil }

// ErrWrite is returned when the sink fails or accepts a short write.
var ErrWrite = errors.New("wal: sink write failed")

// Append durably writes recs as one atomic batch: either every record
// lands or none does. Seq numbers are assigned only after a successful
// write, so a failed Append consumes no sequence numbers.
func (l *Log) Append(recs ...Record) error {
	if len(recs) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	buf := encode(recs)
	n, err := l.sink.Write(buf)
	if err != nil || n != len(buf) {
		return ErrWrite
	}
	for _, r := range recs {
		l.lastSeq++
		r.Seq = l.lastSeq
		l.records = append(l.records, r)
	}
	return nil
}

// LastSeq returns the highest assigned sequence number.
func (l *Log) LastSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq
}
