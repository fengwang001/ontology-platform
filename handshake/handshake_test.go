package handshake

import (
	"errors"
	"fmt"
	"testing"
)

// TestReferenceModel replays the eight canonical operations from NOTES.md and
// then completes the half-open entries left behind (B, C) exactly as derived.
func TestReferenceModel(t *testing.T) {
	h := New()
	// k 's'=SYN (r1=serverISN,r2=ack), 'a'=ACK (r1=clientISN,r2=serverISN)
	st := []struct {
		k         byte
		src       string
		v, r1, r2 int64
		e         error
	}{
		{'s', "A", 1000, 0, 1001, nil},
		{'s', "A", 1000, 0, 1001, nil}, // duplicate SYN: identical reply, no ISN
		{'s', "B", 2000, 1, 2001, nil},
		{'a', "A", 1, 1000, 0, nil},
		{'a', "A", 1, 0, 0, nil}, // duplicate ACK: no-op, no error
		{'s', "C", 3000, 2, 3001, nil},
		{'s', "C", 9999, 3, 10000, nil}, // differing SYN replaces entry
		{'a', "D", 999, 0, 0, ErrHalfOpen},
	}
	for i, s := range st {
		var x, y int64
		var err error
		if s.k == 's' {
			x, y, err = h.RecvSYN(s.src, s.v)
		} else {
			x, y, err = h.RecvACK(s.src, s.v)
		}
		if !errors.Is(err, s.e) || x != s.r1 || y != s.r2 {
			t.Fatalf("step %d: got (%d,%d,%v) want (%d,%d,%v)", i+1, x, y, err, s.r1, s.r2, s.e)
		}
	}
	if p, _, _ := h.RecvSYN("p", 0); p != 4 { // nextISN consumed 0..3 only
		t.Fatalf("nextISN=%d want 4", p)
	}
	if !h.Established("A") || h.Established("D") {
		t.Fatal("A must be established, D unknown")
	}
	for _, c := range []struct {
		src         string
		ack, cl, sv int64
	}{
		{"B", 2, 2000, 1}, {"C", 4, 9999, 3},
	} {
		if cl, sv, e := h.RecvACK(c.src, c.ack); e != nil || cl != c.cl || sv != c.sv {
			t.Fatalf("%s: (%d,%d,%v) want (%d,%d)", c.src, cl, sv, e, c.cl, c.sv)
		}
	}
}

// TestSequenceNegotiation pins SYN-ACK.ack == clientISN+1, completion at
// ack == serverISN+1, and strictly increasing, never-reused serverISNs.
func TestSequenceNegotiation(t *testing.T) {
	cases := []struct{ seq, synAck, srvISN int64 }{
		{1000, 1001, 0}, {0, 1, 1}, {1 << 40, 1<<40 + 1, 2},
	}
	h := New()
	for i, c := range cases {
		s, a, err := h.RecvSYN(fmt.Sprintf("x%d", i), c.seq)
		if err != nil || a != c.synAck || s != c.srvISN {
			t.Fatalf("case %d: syn=(%d,%d,%v) want srv=%d", i, s, a, err, c.srvISN)
		}
		if cl, sv, err := h.RecvACK(fmt.Sprintf("x%d", i), s+1); err != nil || cl != c.seq || sv != s {
			t.Fatalf("case %d: ack=(%d,%d,%v)", i, cl, sv, err)
		}
	}
}

// TestIdempotency pins duplicate SYN returning the identical SYN-ACK without
// consuming nextISN, and duplicate ACK on an established src being a no-op.
func TestIdempotency(t *testing.T) {
	h := New()
	s1, a1, _ := h.RecvSYN("x", 1000)
	s2, a2, _ := h.RecvSYN("x", 1000)
	if s1 != s2 || a1 != a2 {
		t.Fatalf("dup SYN differs: (%d,%d) vs (%d,%d)", s1, a1, s2, a2)
	}
	if p, _, _ := h.RecvSYN("p", 0); p != 1 { // dup SYN did not consume nextISN
		t.Fatalf("nextISN=%d want 1", p)
	}
	if _, _, e := h.RecvACK("x", s1+1); e != nil {
		t.Fatalf("complete: %v", e)
	}
	if c, s, e := h.RecvACK("x", s1+1); e != nil || c != 0 || s != 0 {
		t.Fatalf("dup ACK not silent no-op: (%d,%d,%v)", c, s, e)
	}
}

// TestRejectedOpsLeaveNoTrace pins that all three distinct rejection errors
// leave every piece of state untouched and the server stays usable.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	h := New()
	syn, _, _ := h.RecvSYN("a", 1000)
	cases := []struct {
		name string
		want error
		fn   func() error
	}{
		{"bad seq", ErrBadSeq, func() error { _, _, e := h.RecvSYN("z", -1); return e }},
		{"bad ack", ErrBadAck, func() error { _, _, e := h.RecvACK("a", syn+9); return e }},
		{"orphan ack", ErrHalfOpen, func() error { _, _, e := h.RecvACK("z", 1); return e }},
	}
	for _, c := range cases {
		if !errors.Is(c.fn(), c.want) || h.Established("a") || h.Established("z") {
			t.Fatalf("%s: wrong error or state changed", c.name)
		}
	}
	if p, _, _ := h.RecvSYN("q", 7); p != 1 { // nextISN untouched; still usable
		t.Fatalf("nextISN=%d want 1", p)
	}
	if c, s, e := h.RecvACK("a", syn+1); e != nil || c != 1000 || s != syn {
		t.Fatalf("a not completable after rejects: (%d,%d,%v)", c, s, e)
	}
}
