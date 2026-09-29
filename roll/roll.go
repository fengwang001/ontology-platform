// Package roll computes a fixed-window rolling hash over a byte stream.
// Every call to Push advances the window by one byte in O(1) time.
package roll

import "errors"

// ErrWindow is returned when the window length is illegal.
var ErrWindow = errors.New("roll: window length must be > 0")

const base = 31

// Hasher is a fixed-size polynomial rolling hash.
//
// For window bytes b0..b(w-1) the hash is
//
//	H = b0*B^(w-1) + b1*B^(w-2) + ... + b(w-1)
//
// modulo 2^32. Push drops b0 and appends the new byte in O(1).
type Hasher struct {
	w      uint32
	high   uint32 // B^(w-1) mod 2^32
	h      uint32
	buf    []byte
	pos    int
	filled int
	// pushes counts "one byte in, one byte out" executions.
	// Unexported: it must never appear in the public API.
	pushes int64
}

// New returns a Hasher with the given window length.
func New(window int) (*Hasher, error) {
	if window <= 0 {
		return nil, ErrWindow
	}
	hr := &Hasher{w: uint32(window), buf: make([]byte, window)}
	hr.high = 1
	for i := 0; i < window-1; i++ {
		hr.high *= base
	}
	return hr, nil
}

// Write appends bytes during the initial fill of the window.
// It panics once the window is full: callers must use Push afterwards.
func (hr *Hasher) Write(b byte) {
	if hr.filled >= len(hr.buf) {
		panic("roll: window already full, use Push")
	}
	hr.buf[hr.filled] = b
	hr.h = hr.h*base + uint32(b)
	hr.filled++
}

// Push advances the window: the oldest byte leaves, b enters.
// It returns false until the window has been filled with Write.
func (hr *Hasher) Push(b byte) bool {
	if hr.filled < len(hr.buf) {
		return false
	}
	out := hr.buf[hr.pos]
	hr.h = (hr.h-uint32(out)*hr.high)*base + uint32(b)
	hr.buf[hr.pos] = b
	hr.pos = (hr.pos + 1) % len(hr.buf)
	hr.pushes++
	return true
}

// Full reports whether the window contains w bytes.
func (hr *Hasher) Full() bool { return hr.filled >= len(hr.buf) }

// Sum32 returns the current hash value.
func (hr *Hasher) Sum32() uint32 { return hr.h }

// Clone returns an independent copy (used for transactional rollback).
func (hr *Hasher) Clone() *Hasher {
	cp := *hr
	cp.buf = append([]byte(nil), hr.buf...)
	return &cp
}
