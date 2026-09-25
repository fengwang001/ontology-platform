// Package api is the public facade over the framing packages.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/esc"
	"ontology/frame"
)

// Sentinel errors, re-exported so callers only import this package.
var (
	ErrBadEscape       = frame.ErrBadEscape
	ErrTruncatedEscape = frame.ErrTruncatedEscape
	ErrTruncatedFrame  = frame.ErrTruncatedFrame
)

// Framer decodes one byte stream into frames. Safe for concurrent use:
// Feed/Flush take the write lock, Frames the read lock.
type Framer struct {
	mu sync.RWMutex
	p  *frame.Parser
}

// New returns a Framer in the OUT state.
func New() *Framer { return &Framer{p: frame.New()} }

// Frame encodes payload into one framed byte stream. Pure function.
func Frame(payload []byte) []byte { return frame.Encode(payload) }

// Feed consumes chunk and returns the frames it completes.
func (f *Framer) Feed(chunk []byte) ([][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.p.Feed(chunk)
}

// Flush reports a truncated escape or frame.
func (f *Framer) Flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.p.Flush()
}

// Frames returns a read-only snapshot of all frames collected so far.
func (f *Framer) Frames() [][]byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.p.Frames()
}

// SelfCheck verifies the four invariants on built-in byte streams.
// It uses only fresh internal parsers and is safe for concurrent use.
func (f *Framer) SelfCheck() error {
	payloads := [][]byte{
		{},
		{0x41},
		{esc.Flag},
		{esc.Esc},
		{esc.Flag, esc.Esc, 0x00, 0xFF},
	}
	var stream []byte
	// Invariant 1 (round-trip) and 3 (lossless delimiting).
	for _, p := range payloads {
		wire := Frame(p)
		body := wire[1 : len(wire)-1]
		for i, b := range body {
			if b == esc.Flag {
				return fmt.Errorf("selfcheck delimit: raw FLAG inside frame")
			}
			if b == esc.Esc && (i+1 >= len(body) || !esc.NeedsEscape(esc.Map(body[i+1]))) {
				return fmt.Errorf("selfcheck delimit: bad escape inside frame")
			}
		}
		got, err := New().Feed(wire)
		if err != nil || len(got) != 1 || !bytes.Equal(got[0], p) {
			return fmt.Errorf("selfcheck roundtrip %x: got %x err %v", p, got, err)
		}
		stream = append(stream, wire...)
	}
	// Invariant 2 (split-point independence): every single split point.
	ref := New()
	if _, err := ref.Feed(stream); err != nil {
		return fmt.Errorf("selfcheck split ref: %v", err)
	}
	want := ref.Frames()
	for i := 1; i < len(stream); i++ {
		g := New()
		if _, err := g.Feed(stream[:i]); err != nil {
			return fmt.Errorf("selfcheck split %d: %v", i, err)
		}
		if _, err := g.Feed(stream[i:]); err != nil {
			return fmt.Errorf("selfcheck split %d: %v", i, err)
		}
		if err := g.Flush(); err != nil {
			return fmt.Errorf("selfcheck split %d flush: %v", i, err)
		}
		if !equalFrames(g.Frames(), want) {
			return fmt.Errorf("selfcheck split %d: frames differ", i)
		}
	}
	// Invariant 4 (failure leaves no trace) + distinguishable errors.
	bad := New()
	if _, err := bad.Feed([]byte{esc.Flag, esc.Esc, esc.Flag}); !errors.Is(err, ErrBadEscape) {
		return fmt.Errorf("selfcheck bad escape: %v", err)
	}
	if n := len(bad.Frames()); n != 0 {
		return fmt.Errorf("selfcheck bad escape: %d frames leaked", n)
	}
	if _, err := bad.Feed([]byte{0x41, esc.Flag}); err != nil { // still usable
		return fmt.Errorf("selfcheck reuse after rejection: %v", err)
	}
	te, tf := New(), New()
	if _, err := te.Feed([]byte{esc.Flag, esc.Esc}); err != nil {
		return err
	}
	if _, err := tf.Feed([]byte{esc.Flag, 0x41}); err != nil {
		return err
	}
	if err := te.Flush(); !errors.Is(err, ErrTruncatedEscape) {
		return fmt.Errorf("selfcheck truncated escape: %v", err)
	}
	if err := tf.Flush(); !errors.Is(err, ErrTruncatedFrame) {
		return fmt.Errorf("selfcheck truncated frame: %v", err)
	}
	if errors.Is(ErrBadEscape, ErrTruncatedEscape) || errors.Is(ErrTruncatedEscape, ErrTruncatedFrame) {
		return fmt.Errorf("selfcheck: sentinel errors not distinct")
	}
	if !frame.SinglePass(1000) {
		return fmt.Errorf("selfcheck: not single-pass")
	}
	return nil
}

func equalFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
