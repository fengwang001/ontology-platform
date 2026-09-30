// Package flowctl implements a two-level (connection + stream) flow-control
// accountant for a multi-stream sender.
package flowctl

import (
	"errors"
	"sync"
)

// MaxWindow is the upper bound for every window (2^31 - 1).
const MaxWindow int64 = 1<<31 - 1

// ConnID is the reserved stream identifier used to address the
// connection-level window in Increment.
const ConnID int64 = 0

var (
	ErrInvalidConnWindow    = errors.New("flowctl: connection window initial value out of range")
	ErrInvalidInitialWindow = errors.New("flowctl: initial stream window out of range")
	ErrInvalidMaxFrame      = errors.New("flowctl: max frame size out of range")
	ErrStreamNotFound       = errors.New("flowctl: stream does not exist")
	ErrStreamClosed         = errors.New("flowctl: stream is closed")
	ErrNonPositiveAmount    = errors.New("flowctl: amount must be positive")
	ErrExceedsMaxFrame      = errors.New("flowctl: amount exceeds max frame size")
	ErrExceedsConnWindow    = errors.New("flowctl: amount exceeds connection window")
	ErrExceedsStreamWindow  = errors.New("flowctl: amount exceeds stream window")
	ErrConnWindowOverflow   = errors.New("flowctl: connection window increment overflows")
	ErrStreamWindowOverflow = errors.New("flowctl: stream window increment overflows")
	ErrAdjustWindowOverflow = errors.New("flowctl: initial window adjustment overflows a stream window")
	ErrInvalidStreamID      = errors.New("flowctl: stream id must be positive")
	ErrStreamIDUsed         = errors.New("flowctl: stream id already used")
)

type stream struct {
	window int64
	closed bool
}

// Controller is a concurrency-safe two-level flow-control accountant.
type Controller struct {
	mu            sync.Mutex
	connWindow    int64
	initialWindow int64
	maxFrame      int64
	streams       map[int64]*stream
}

// New creates a Controller. connWindow and initialWindow must be within
// [0, MaxWindow]; maxFrame must be within [1, MaxWindow].
func New(connWindow, initialWindow, maxFrame int64) (*Controller, error) {
	if connWindow < 0 || connWindow > MaxWindow {
		return nil, ErrInvalidConnWindow
	}
	if initialWindow < 0 || initialWindow > MaxWindow {
		return nil, ErrInvalidInitialWindow
	}
	if maxFrame <= 0 || maxFrame > MaxWindow {
		return nil, ErrInvalidMaxFrame
	}
	return &Controller{
		connWindow:    connWindow,
		initialWindow: initialWindow,
		maxFrame:      maxFrame,
		streams:       make(map[int64]*stream),
	}, nil
}

// OpenStream registers a new stream id with the current initial window.
func (c *Controller) OpenStream(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id <= 0 {
		return ErrInvalidStreamID
	}
	if _, ok := c.streams[id]; ok {
		return ErrStreamIDUsed
	}
	c.streams[id] = &stream{window: c.initialWindow}
	return nil
}

// CloseStream closes an open stream. Its id is never reusable and its
// unused window is not returned to the connection window.
func (c *Controller) CloseStream(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.streams[id]
	if !ok {
		return ErrStreamNotFound
	}
	if s.closed {
		return ErrStreamClosed
	}
	s.closed = true
	return nil
}

// Send deducts n bytes from both the connection window and the stream
// window, all-or-nothing.
func (c *Controller) Send(id, n int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.streams[id]
	if !ok {
		return ErrStreamNotFound
	}
	if s.closed {
		return ErrStreamClosed
	}
	if n <= 0 {
		return ErrNonPositiveAmount
	}
	if n > c.maxFrame {
		return ErrExceedsMaxFrame
	}
	if n > c.connWindow {
		return ErrExceedsConnWindow
	}
	if n > s.window {
		return ErrExceedsStreamWindow
	}
	c.connWindow -= n
	s.window -= n
	return nil
}

// Increment adds delta to the connection window (id == ConnID) or to a
// stream window.
func (c *Controller) Increment(id, delta int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == ConnID {
		if delta <= 0 {
			return ErrNonPositiveAmount
		}
		if c.connWindow+delta > MaxWindow {
			return ErrConnWindowOverflow
		}
		c.connWindow += delta
		return nil
	}
	s, ok := c.streams[id]
	if !ok {
		return ErrStreamNotFound
	}
	if s.closed {
		return ErrStreamClosed
	}
	if delta <= 0 {
		return ErrNonPositiveAmount
	}
	if s.window+delta > MaxWindow {
		return ErrStreamWindowOverflow
	}
	s.window += delta
	return nil
}

// AdjustInitial replaces the initial window with newI and shifts every
// open stream window by newI - oldI (possibly making them negative).
func (c *Controller) AdjustInitial(newI int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if newI < 0 || newI > MaxWindow {
		return ErrInvalidInitialWindow
	}
	delta := newI - c.initialWindow
	for _, s := range c.streams {
		if s.closed {
			continue
		}
		if s.window+delta > MaxWindow {
			return ErrAdjustWindowOverflow
		}
	}
	for _, s := range c.streams {
		if s.closed {
			continue
		}
		s.window += delta
	}
	c.initialWindow = newI
	return nil
}

// ConnWindow reports the current connection window.
func (c *Controller) ConnWindow() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connWindow
}

// StreamWindow reports the current window of a stream.
func (c *Controller) StreamWindow(id int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.streams[id]
	if !ok {
		return 0, ErrStreamNotFound
	}
	return s.window, nil
}

// InitialWindow reports the current initial window given to new streams.
func (c *Controller) InitialWindow() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initialWindow
}
