package sink

import (
	"bytes"
	"errors"
)

var (
	// ErrBackpressure means the downstream is temporarily unable to accept bytes.
	ErrBackpressure = errors.New("sink: backpressure")
	// ErrDisconnect means the downstream connection broke at this byte point.
	ErrDisconnect = errors.New("sink: disconnected")
)

// Sink is the downstream byte consumer.
type Sink interface {
	Write(p []byte) (int, error)
}

// Memory is a perfect sink that always accepts every byte.
type Memory struct {
	buf bytes.Buffer
}

// Write implements Sink.
func (m *Memory) Write(p []byte) (int, error) { return m.buf.Write(p) }

// Bytes returns a copy of all received bytes.
func (m *Memory) Bytes() []byte {
	out := make([]byte, m.buf.Len())
	copy(out, m.buf.Bytes())
	return out
}

// Scripted injects short writes, backpressure, disconnects and write errors.
//
// MaxAccept caps bytes accepted per Write call (0 = unlimited). Quota is the
// flow-control window: while it is 0 every Write returns ErrBackpressure;
// refill with AddQuota. CutAfter (>=0) disconnects once total accepted bytes
// reach it. FailAt (>=0) returns FailErr once total accepted bytes reach it.
// Pass -1 to CutAfter and FailAt to disable them.
type Scripted struct {
	MaxAccept int
	Quota     int64
	CutAfter  int64
	FailAt    int64
	FailErr   error

	buf   bytes.Buffer
	total int64
}

// AddQuota refills the flow-control window.
func (s *Scripted) AddQuota(n int64) { s.Quota += n }

// Total returns confirmed accepted bytes.
func (s *Scripted) Total() int64 { return s.total }

// Bytes returns a copy of all received bytes.
func (s *Scripted) Bytes() []byte {
	out := make([]byte, s.buf.Len())
	copy(out, s.buf.Bytes())
	return out
}

// Write implements Sink.
func (s *Scripted) Write(p []byte) (int, error) {
	if s.Quota <= 0 {
		return 0, ErrBackpressure
	}
	n := len(p)
	if s.MaxAccept > 0 && n > s.MaxAccept {
		n = s.MaxAccept
	}
	if int64(n) > s.Quota {
		n = int(s.Quota)
	}
	err := error(nil)
	if s.CutAfter >= 0 && s.total+int64(n) > s.CutAfter {
		n = int(s.CutAfter - s.total)
		err = ErrDisconnect
	}
	if n < 0 {
		n = 0
	}
	if err == nil && s.FailAt >= 0 && s.FailErr != nil && s.total+int64(n) >= s.FailAt {
		err = s.FailErr
	}
	s.buf.Write(p[:n])
	s.total += int64(n)
	s.Quota -= int64(n)
	return n, err
}

var _ Sink = (*Memory)(nil)
var _ Sink = (*Scripted)(nil)
