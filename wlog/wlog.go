// Package wlog implements an append-only sequence log.
package wlog

import (
	"errors"
	"sync"
)

// ErrEmptyKey is returned when a write has an empty key.
var ErrEmptyKey = errors.New("wlog: empty key")

// Entry is one log record. Seq is globally increasing from 1.
type Entry struct {
	Seq int
	Key string
	Val int
}

// Log is an append-only log; rejected writes never consume a Seq.
type Log struct {
	mu      sync.Mutex
	entries []Entry // entries[i].Seq == i+1
}

// New returns an empty log.
func New() *Log { return &Log{} }

// Write validates then appends, returning the allocated Seq.
func (l *Log) Write(key string, val int) (int, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := len(l.entries) + 1
	l.entries = append(l.entries, Entry{Seq: seq, Key: key, Val: val})
	return seq, nil
}

// Entry returns the entry with the given Seq, locating it directly.
func (l *Log) Entry(seq int) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq < 1 || seq > len(l.entries) {
		return Entry{}, false
	}
	return l.entries[seq-1], true
}

// MaxSeq returns the largest allocated Seq (0 when empty).
func (l *Log) MaxSeq() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
