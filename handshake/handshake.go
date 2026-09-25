// Package handshake implements the passive (server) side SYN/SYN-ACK/ACK
// event handling on top of the half-open table in package hs.
package handshake

import (
	"errors"
	"sync"

	"ontology/hs"
)

// Sentinel errors; all handshake failures are decidable with errors.Is.
var (
	// ErrBadAck: src is half-open but ack != serverISN+1.
	ErrBadAck = errors.New("handshake: ack does not acknowledge serverISN+1")
	// ErrHalfOpen: ACK for a src that is neither half-open nor established.
	ErrHalfOpen = errors.New("handshake: half-open/orphan ack, no such connection")
	// ErrBadSeq: SYN carried a negative client sequence number.
	ErrBadSeq = errors.New("handshake: negative client sequence number")
)

// Handler owns the half-open table and the established connection set.
type Handler struct {
	mu          sync.Mutex
	half        *hs.Table
	established map[string]hs.Conn
}

// New returns a Handler with nextISN == 0 and empty tables.
func New() *Handler {
	return &Handler{half: hs.NewTable(), established: map[string]hs.Conn{}}
}

// RecvSYN handles a client SYN: new connection, identical retransmission, or
// replacement by a half-open entry with a different clientISN.
func (h *Handler) RecvSYN(src string, seq int64) (serverISN, ack int64, err error) {
	if seq < 0 { // rejected before any state is touched
		return 0, 0, ErrBadSeq
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.half.Lookup(src); ok && c.ClientISN == seq {
		return c.ServerISN, c.ClientISN + 1, nil // retransmit: identical SYN-ACK
	}
	c := h.half.Open(src, seq) // new, or replacement of a differing half-open entry
	return c.ServerISN, c.ClientISN + 1, nil
}

// RecvACK handles a client ACK: completion, idempotent duplicate, bad ack, or
// an orphan ack for an unknown src.
func (h *Handler) RecvACK(src string, ack int64) (clientISN, serverISN int64, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.half.Lookup(src); ok {
		if ack != c.ServerISN+1 { // SYN consumes one sequence number
			return 0, 0, ErrBadAck
		}
		h.half.Delete(src)
		h.established[src] = c
		return c.ClientISN, c.ServerISN, nil
	}
	if _, ok := h.established[src]; ok {
		return 0, 0, nil // established: duplicate ACK is an idempotent no-op
	}
	return 0, 0, ErrHalfOpen
}

// Established reports whether src has completed its handshake.
func (h *Handler) Established(src string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.established[src]
	return ok
}
