// Package sink abstracts the downstream writer and provides injectable
// fakes: short writes, backpressure, disconnects, and write errors.
package sink

import "errors"

// Sink is the downstream byte consumer. Write may confirm fewer bytes than
// offered (short write); a non-nil error after n>0 still confirms those n.
type Sink interface {
	Write(p []byte) (int, error)
}

var (
	// ErrBackpressure means "temporarily unwritable, retry later"; n must be 0.
	ErrBackpressure = errors.New("sink: backpressure")
	// ErrDisconnected means the connection broke; bytes reported via n are
	// still confirmed, the rest must be resumed from a checkpoint.
	ErrDisconnected = errors.New("sink: disconnected")
)

// Recorder accepts everything and records the bytes.
type Recorder struct {
	Buf []byte
}

// Write implements Sink.
func (r *Recorder) Write(p []byte) (int, error) {
	r.Buf = append(r.Buf, p...)
	return len(p), nil
}

// ShortWriter accepts at most Max bytes per call, recording them.
type ShortWriter struct {
	Max int
	Buf []byte
}

// Write implements Sink.
func (s *ShortWriter) Write(p []byte) (int, error) {
	if len(p) > s.Max {
		p = p[:s.Max]
	}
	s.Buf = append(s.Buf, p...)
	return len(p), nil
}

// Gate returns ErrBackpressure while Closed, otherwise records everything.
type Gate struct {
	Closed bool
	Buf    []byte
}

// Write implements Sink.
func (g *Gate) Write(p []byte) (int, error) {
	if g.Closed {
		return 0, ErrBackpressure
	}
	g.Buf = append(g.Buf, p...)
	return len(p), nil
}

// Conn confirms up to Limit bytes in total, then reports ErrDisconnected.
// Each call confirms at most Per bytes, so disconnects can land anywhere.
type Conn struct {
	Limit int
	Per   int
	Buf   []byte
}

// Write implements Sink.
func (c *Conn) Write(p []byte) (int, error) {
	remaining := c.Limit - len(c.Buf)
	if remaining <= 0 {
		return 0, ErrDisconnected
	}
	n := len(p)
	if n > remaining {
		n = remaining
	}
	if c.Per > 0 && n > c.Per {
		n = c.Per
	}
	c.Buf = append(c.Buf, p[:n]...)
	if len(c.Buf) >= c.Limit {
		return n, ErrDisconnected
	}
	return n, nil
}

// Failer returns a fixed error without confirming anything.
type Failer struct {
	Err error
}

// Write implements Sink.
func (f *Failer) Write([]byte) (int, error) { return 0, f.Err }
