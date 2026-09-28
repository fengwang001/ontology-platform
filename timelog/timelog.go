package timelog

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidCapacity    = errors.New("timelog: invalid capacity")
	ErrNegativeTimestamp  = errors.New("timelog: negative timestamp")
	ErrEmptyPayload       = errors.New("timelog: empty payload")
	ErrCapacityExceeded   = errors.New("timelog: log capacity exceeded")
	ErrPositionOutOfRange = errors.New("timelog: position out of range")
)

// Message is one append-only log record.
type Message struct {
	Timestamp int64
	Payload   string
}

type indexEntry struct {
	timestamp int64
	position  int
}

// Log is an append-only log whose message timestamps are not guaranteed
// monotonic, plus a sparse strictly-increasing timestamp index.
type Log struct {
	mu        sync.RWMutex
	messages  []Message
	index     []indexEntry
	maxTS     int64
	hasMax    bool
	capacity  int
}

// New creates an empty log holding at most capacity messages.
func New(capacity int) (*Log, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Log{
		messages: make([]Message, 0, capacity),
		index:    make([]indexEntry, 0, capacity),
		capacity: capacity,
	}, nil
}

// Append appends a message and returns its position (0-based). A message is
// added to the time index only when its timestamp is strictly greater than
// every timestamp seen before; equal timestamps are not indexed. All argument
// validation happens before any state mutation, so a rejected append leaves
// the log end position, the index and existing messages untouched.
func (l *Log) Append(timestamp int64, payload string) (int, error) {
	if timestamp < 0 {
		return 0, ErrNegativeTimestamp
	}
	if payload == "" {
		return 0, ErrEmptyPayload
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.messages) >= l.capacity {
		return 0, ErrCapacityExceeded
	}
	position := len(l.messages)
	l.messages = append(l.messages, Message{Timestamp: timestamp, Payload: payload})
	if !l.hasMax || timestamp > l.maxTS {
		l.index = append(l.index, indexEntry{timestamp: timestamp, position: position})
		l.maxTS = timestamp
		l.hasMax = true
	}
	return position, nil
}

// Query returns the smallest position whose message timestamp is not smaller
// than timestamp. If no such message exists it returns the log end position
// (one past the last message) with found=false. The position is located via a
// binary search over the sparse time index; messages are never scanned.
//
// Correctness against a naive scan: the first message with ts >= t is
// necessarily a strict record high (every earlier message has ts < t), so it
// is always present in the index.
func (l *Log) Query(timestamp int64) (position int, found bool, err error) {
	if timestamp < 0 {
		return 0, false, ErrNegativeTimestamp
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	i := sort.Search(len(l.index), func(i int) bool {
		return l.index[i].timestamp >= timestamp
	})
	if i == len(l.index) {
		return len(l.messages), false, nil
	}
	return l.index[i].position, true, nil
}

// End returns the log end position (number of appended messages).
func (l *Log) End() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.messages)
}

// At returns the message at position. It exists for callers that need to
// re-read starting at a position returned by Query and for verification.
func (l *Log) At(position int) (Message, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if position < 0 || position >= len(l.messages) {
		return Message{}, ErrPositionOutOfRange
	}
	return l.messages[position], nil
}

// Len returns the number of stored messages.
func (l *Log) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.messages)
}

// IndexLen returns the number of entries in the sparse time index.
func (l *Log) IndexLen() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.index)
}
