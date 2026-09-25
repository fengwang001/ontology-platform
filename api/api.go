// Package api is the external entry point to the passive-side handshake
// state machine. It depends only downward on handshake and hs.
package api

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/handshake"
	"ontology/hs"
)

// Decidable sentinel errors, re-exported for callers of this package.
var (
	ErrBadAck   = handshake.ErrBadAck   // half-open src, ack != serverISN+1
	ErrHalfOpen = handshake.ErrHalfOpen // ACK for an unknown src
	ErrBadSeq   = handshake.ErrBadSeq   // negative SYN sequence number
)

// API is the concurrent-safe handshake server state.
type API struct {
	h *handshake.Handler
}

// New returns an empty server with nextISN == 0.
func New() *API { return &API{h: handshake.New()} }

// RecvSYN handles a client SYN and returns (serverISN, acknowledgement).
func (a *API) RecvSYN(src string, seq int64) (serverISN, ack int64, err error) {
	return a.h.RecvSYN(src, seq)
}

// RecvACK handles a client ACK and returns the completed connection's ISNs.
func (a *API) RecvACK(src string, ack int64) (clientISN, serverISN int64, err error) {
	return a.h.RecvACK(src, ack)
}

// Established reports whether src has completed the handshake.
func (a *API) Established(src string) bool { return a.h.Established(src) }

// SelfCheck runs the built-in handshake sequences and verifies all four
// invariants: rule conformance over the eight canonical steps (and the
// probes around them), sequence negotiation, idempotency, and no-trace
// failures, plus the O(1) probe and concurrent non-crossing guarantees.
func SelfCheck() error {
	a := New()
	// Canonical eight operations from NOTES.md.
	type op struct {
		kind        string
		src         string
		v, isn, ack int64
		wantErr     error
	}
	ops := []op{
		{"syn", "A", 1000, 0, 1001, nil},
		{"syn", "A", 1000, 0, 1001, nil}, // duplicate SYN: identical reply
		{"syn", "B", 2000, 1, 2001, nil},
		{"ack", "A", 1, 1000, 0, nil},
		{"ack", "A", 1, 0, 0, nil}, // duplicate ACK: no-op, no error
		{"syn", "C", 3000, 2, 3001, nil},
		{"syn", "C", 9999, 3, 10000, nil}, // differing SYN replaces half-open entry
		{"ack", "D", 999, 0, 0, ErrHalfOpen},
	}
	for i, o := range ops {
		var isn, ack int64
		var err error
		if o.kind == "syn" {
			isn, ack, err = a.RecvSYN(o.src, o.v)
		} else {
			isn, ack, err = a.RecvACK(o.src, o.v)
		}
		if !errors.Is(err, o.wantErr) || isn != o.isn || ack != o.ack {
			return fmt.Errorf("selfcheck step %d: got (%d,%d,%v), want (%d,%d,%v)", i+1, isn, ack, err, o.isn, o.ack, o.wantErr)
		}
	}
	if probe, _, err := a.RecvSYN("probe", 0); err != nil || probe != 4 {
		return fmt.Errorf("selfcheck nextISN=%d, want 4", probe)
	}
	// The three failures are distinct and leave no trace behind.
	if errors.Is(ErrBadAck, ErrHalfOpen) || errors.Is(ErrBadAck, ErrBadSeq) || errors.Is(ErrHalfOpen, ErrBadSeq) {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	b := New()
	b.RecvSYN("a", 1000)
	for _, c := range []struct {
		e  error
		fn func() error
	}{
		{ErrBadSeq, func() error { _, _, e := b.RecvSYN("z", -1); return e }},
		{ErrBadAck, func() error { _, _, e := b.RecvACK("a", 9); return e }},
		{ErrHalfOpen, func() error { _, _, e := b.RecvACK("z", 1); return e }},
	} {
		if !errors.Is(c.fn(), c.e) {
			return fmt.Errorf("selfcheck: want %v", c.e)
		}
	}
	if probe, _, _ := b.RecvSYN("q", 0); probe != 1 {
		return fmt.Errorf("selfcheck: rejected op consumed ISN, probe=%d", probe)
	}
	if err := hs.VerifyProbeConstant(); err != nil {
		return err
	}
	return concurrentSelfCheck()
}

func concurrentSelfCheck() error {
	const N = 64
	a := New()
	isns := make([]int64, N)
	var crossed atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			src := fmt.Sprintf("g%02d", g)
			isn, _, err := a.RecvSYN(src, int64(1000+g))
			if err != nil {
				crossed.Store(true)
				return
			}
			isns[g] = isn
			c, s, err := a.RecvACK(src, isn+1)
			if err != nil || c != int64(1000+g) || s != isn {
				crossed.Store(true)
			}
		}(g)
	}
	wg.Wait()
	if crossed.Load() {
		return errors.New("selfcheck: concurrent handshake crossed")
	}
	seen := map[int64]bool{}
	for g := 0; g < N; g++ {
		if !a.Established(fmt.Sprintf("g%02d", g)) || seen[isns[g]] {
			return errors.New("selfcheck: concurrent handshake crossed or ISN reused")
		}
		seen[isns[g]] = true
	}
	return nil
}
