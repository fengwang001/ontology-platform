package fragmentlog

import (
	"io"
	"sync"
)

// Log is an in-memory fragmented record log. Append and Reader may be
// called concurrently; the result is equivalent to some serial order, and
// because each Append is atomic the committed byte stream always ends at a
// record boundary.
type Log struct {
	mu   sync.RWMutex
	buf  []byte
	w    *Writer
	blks int
}

// NewLog returns an in-memory Log with the given block size.
func NewLog(blockSize int) (*Log, error) {
	l := &Log{blks: blockSize}
	w, err := NewWriter(writerFunc(func(p []byte) (int, error) {
		l.buf = append(l.buf, p...)
		return len(p), nil
	}), blockSize)
	if err != nil {
		return nil, err
	}
	l.w = w
	return l, nil
}

// Append appends rec and returns the offset of its first fragment header.
func (l *Log) Append(rec []byte) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Append(rec)
}

// Len reports the number of committed bytes.
func (l *Log) Len() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return int64(len(l.buf))
}

// Bytes returns a snapshot of the committed bytes.
func (l *Log) Bytes() []byte {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]byte(nil), l.buf...)
}

// Reader returns a Reader over the live log. It observes records committed
// so far; reaching the currently committed end yields io.EOF (more records
// may be appended afterwards, visible to a fresh Reader).
func (l *Log) Reader() *Reader {
	rd, err := NewReader(&logReader{l: l}, l.blks)
	if err != nil {
		panic(err) // block size was validated by NewLog
	}
	return rd
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type logReader struct {
	l   *Log
	pos int64
}

func (r *logReader) Read(p []byte) (int, error) {
	r.l.mu.RLock()
	defer r.l.mu.RUnlock()
	if r.pos >= int64(len(r.l.buf)) {
		return 0, io.EOF
	}
	n := copy(p, r.l.buf[r.pos:])
	r.pos += int64(n)
	return n, nil
}
