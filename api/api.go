// Package api is the public facade: node events, messages, queries and
// the self-check. It depends on trace only.
package api

import (
	"errors"
	"math"
	"math/rand"

	"ontology/hlc"
	"ontology/trace"
)

// API is the handle returned by New. All methods are goroutine-safe.
type API struct{ tr *trace.Trace }

// New creates n nodes; n<=0, maxC<=0 or maxOffset<0 fail with trace.ErrParam.
func New(n int, maxOffset, maxC int64) (*API, error) {
	tr, err := trace.New(n, maxOffset, maxC)
	if err != nil {
		return nil, err
	}
	return &API{tr: tr}, nil
}

// Local records a local event and returns its (l, c).
func (a *API) Local(node int, pt int64) (hlc.T, error) { return a.tr.Local(node, pt) }

// Send records a send event and returns its (l, c) and the message id.
func (a *API) Send(from, to int, pt int64) (hlc.T, int64, error) {
	return a.tr.Send(from, to, pt)
}

// Recv records the receive of message id on node to and returns its (l, c).
func (a *API) Recv(to int, id int64, pt int64) (hlc.T, error) { return a.tr.Recv(to, id, pt) }

// CountUpTo returns how many events of node have timestamp <= (l, c).
func (a *API) CountUpTo(node int, l, c int64) (int, error) {
	return a.tr.CountUpTo(node, hlc.T{L: l, C: c})
}

// SelfCheck verifies the four invariants on built-in event sequences:
// deterministic random readings with clock rollback interleaved with random
// send/recv, then failure atomicity of every rejection class.
func (a *API) SelfCheck() error {
	tr, err := trace.New(4, 50, 1000)
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(297))
	type m struct {
		to int
		id int64
	}
	var inflight []m
	pt := make([]int64, 4)
	for range 400 {
		node := rng.Intn(4)
		pt[node] = max(0, pt[node]+rng.Int63n(11)-5) // random walk with rollback
		switch rng.Intn(3) {
		case 0:
			_, err = tr.Local(node, pt[node])
		case 1:
			var id int64
			to := rng.Intn(4)
			if _, id, err = tr.Send(node, to, pt[node]); err == nil {
				inflight = append(inflight, m{to, id})
			}
		default:
			if len(inflight) == 0 {
				continue
			}
			j := rng.Intn(len(inflight))
			mm := inflight[j]
			inflight = append(inflight[:j], inflight[j+1:]...)
			if _, err = tr.Recv(mm.to, mm.id, pt[mm.to]); errors.Is(err, hlc.ErrOffset) {
				inflight = append(inflight, mm) // rejected: still receivable
				err = nil
			}
		}
		if err != nil {
			return err
		}
	}
	if err := tr.Verify(); err != nil { // invariants 1-3
		return err
	}
	return checkFailureAtomicity() // invariant 4
}

// checkFailureAtomicity: every rejected class leaves state untouched, and a
// rejected message is still receivable afterwards (at exactly maxOffset).
func checkFailureAtomicity() error {
	tr, _ := trace.New(2, 5, 100)
	if _, err := tr.Local(0, 3); err != nil {
		return err
	}
	_, id, err := tr.Send(0, 1, 10)
	if err != nil {
		return err
	}
	total := func() int {
		n := 0
		for node := range 2 {
			c, _ := tr.CountUpTo(node, hlc.T{L: math.MaxInt64, C: math.MaxInt64})
			n += c
		}
		return n
	}
	before := total()
	bads := []func() error{
		func() error { _, e := tr.Local(9, 1); return e },
		func() error { _, e := tr.Local(0, -1); return e },
		func() error { _, e := tr.Recv(1, 999, 4); return e },
		func() error { _, e := tr.Recv(0, id, 10); return e },
		func() error { _, e := tr.Recv(1, id, 4); return e }, // 10-4>5: offset
	}
	for _, bad := range bads {
		if bad() == nil {
			return errors.New("rejected op succeeded")
		}
		if total() != before {
			return errors.New("rejected op mutated state")
		}
	}
	small, _ := trace.New(1, 5, 1)
	_, _ = small.Local(0, 1)
	_, _ = small.Local(0, 1)
	if _, err := small.Local(0, 1); !errors.Is(err, hlc.ErrCounter) {
		return errors.New("counter overflow not rejected")
	}
	if _, err := tr.Recv(1, id, 5); err != nil { // 10-5==maxOffset: accepted
		return err
	}
	if _, err := tr.Recv(1, id, 6); !errors.Is(err, trace.ErrReceived) {
		return errors.New("double receive not rejected")
	}
	return tr.Verify()
}
