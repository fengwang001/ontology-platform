// Package redlock implements a Redlock-style majority lease lock
// acquisition engine with an injected, fully deterministic clock.
//
// The engine talks to N independent nodes. Each node stores at most one
// record per resource: (token, expiry). A client clock c is advanced
// deterministically from the caller-supplied start time using the
// per-node round-trip times, so identical operation sequences replay to
// identical tokens, grant counts and node records.
package redlock

import (
	"errors"
	"fmt"
	"sync"
)

// maxTime is the largest legal value for start/now timestamps (10^15).
const maxTime = int64(1_000_000_000_000_000)

var (
	// ErrInvalidConfig is returned by New when N, Tn, D or MaxTTL are out
	// of range; the whole configuration is rejected.
	ErrInvalidConfig = errors.New("redlock: invalid config")
	// ErrInvalidArgument is returned when resource names, ttl, rtt,
	// tokens or node indexes are invalid.
	ErrInvalidArgument = errors.New("redlock: invalid argument")
	// ErrInvalidTime is returned when start/now is outside [0, 10^15].
	ErrInvalidTime = errors.New("redlock: invalid time")
	// ErrClockRewind is returned when start/now is smaller than the
	// engine clock watermark T.
	ErrClockRewind = errors.New("redlock: clock regression")
)

// record is a single node's lease entry for one resource.
type record struct {
	token  int64
	expiry int64
}

// node is one independent lock node.
type node struct {
	records map[string]record
	quiet   int64 // requests arriving before this instant are rejected
}

// Engine is a majority lease lock acquirer. All methods are safe for
// concurrent use; the result is equivalent to some serial order.
type Engine struct {
	mu        sync.Mutex
	n         int
	tn        int64
	drift     int64
	maxTTL    int64
	nodes     []node
	clock     int64 // watermark T
	nextToken int64
}

// New builds an engine with n independent nodes (1..9), per-node timeout
// tn (1..10^6), clock drift per mille drift (0..1000) and maximum lease
// ttl maxTTL (1..10^9). Any out-of-range value rejects the whole config.
func New(n int, tn, drift, maxTTL int64) (*Engine, error) {
	if n < 1 || n > 9 {
		return nil, fmt.Errorf("%w: N=%d not in [1,9]", ErrInvalidConfig, n)
	}
	if tn < 1 || tn > 1_000_000 {
		return nil, fmt.Errorf("%w: Tn=%d not in [1,10^6]", ErrInvalidConfig, tn)
	}
	if drift < 0 || drift > 1000 {
		return nil, fmt.Errorf("%w: D=%d not in [0,1000]", ErrInvalidConfig, drift)
	}
	if maxTTL < 1 || maxTTL > 1_000_000_000 {
		return nil, fmt.Errorf("%w: MaxTTL=%d not in [1,10^9]", ErrInvalidConfig, maxTTL)
	}
	e := &Engine{
		n:         n,
		tn:        tn,
		drift:     drift,
		maxTTL:    maxTTL,
		nodes:     make([]node, n),
		nextToken: 1,
	}
	for i := range e.nodes {
		e.nodes[i].records = make(map[string]record)
	}
	return e, nil
}

// AcquireResult reports the outcome of one Acquire call.
type AcquireResult struct {
	Token    int64 // token k taken by this call (success or failure)
	Success  bool  // quorum granted and validity strictly positive
	Grants   int   // g: grants from reachable, non-timed-out nodes
	Validity int64 // v = ttl - (cend-start) - dr
	Until    int64 // start + ttl - dr (== cend + v)
	Cend     int64 // client clock after processing all nodes
}

// Acquire requests a lease on res for ttl from every node in order.
//
// The client clock c starts at start. For node i: rtt[i] == -1 means
// unreachable (node not executed, c += Tn); otherwise the request
// arrives at a = c + rtt[i]/2 and executes, then c += rtt[i] when
// rtt[i] <= Tn, or c += Tn on timeout (rtt[i] > Tn). A node rejects when
// a < quiet, or when it already holds res with expiry > a (NX, even for
// the same client's old token); otherwise it stores (k, a+ttl). Only
// grants with rtt[i] <= Tn count toward g. The call succeeds iff
// g >= floor(N/2)+1 and v > 0; on failure every node (including
// unreachable and timed-out ones) is sent a release for token k.
func (e *Engine) Acquire(res string, ttl, start int64, rtt []int64) (AcquireResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if res == "" {
		return AcquireResult{}, fmt.Errorf("%w: empty resource", ErrInvalidArgument)
	}
	if ttl < 1 || ttl > e.maxTTL {
		return AcquireResult{}, fmt.Errorf("%w: ttl=%d not in [1,%d]", ErrInvalidArgument, ttl, e.maxTTL)
	}
	if len(rtt) != e.n {
		return AcquireResult{}, fmt.Errorf("%w: rtt length %d, want %d", ErrInvalidArgument, len(rtt), e.n)
	}
	for _, r := range rtt {
		if r < -1 || r > 2*e.tn {
			return AcquireResult{}, fmt.Errorf("%w: rtt=%d not -1 or in [0,%d]", ErrInvalidArgument, r, 2*e.tn)
		}
	}
	if start < 0 || start > maxTime {
		return AcquireResult{}, fmt.Errorf("%w: start=%d not in [0,10^15]", ErrInvalidTime, start)
	}
	if start < e.clock {
		return AcquireResult{}, fmt.Errorf("%w: start=%d < T=%d", ErrClockRewind, start, e.clock)
	}

	k := e.nextToken
	e.nextToken++

	c := start
	grants := 0
	for i := 0; i < e.n; i++ {
		r := rtt[i]
		if r == -1 {
			c += e.tn
			continue
		}
		a := c + r/2
		granted := false
		nd := &e.nodes[i]
		if a >= nd.quiet {
			if rec, ok := nd.records[res]; !ok || rec.expiry <= a {
				nd.records[res] = record{token: k, expiry: a + ttl}
				granted = true
			}
		}
		if r <= e.tn {
			c += r
			if granted {
				grants++
			}
		} else {
			c += e.tn
		}
	}
	cend := c
	dr := ttl*e.drift/1000 + 2
	validity := ttl - (cend - start) - dr
	until := start + ttl - dr
	success := grants >= e.n/2+1 && validity > 0
	if !success {
		e.releaseAll(res, k)
	}
	e.clock = cend
	return AcquireResult{
		Token:    k,
		Success:  success,
		Grants:   grants,
		Validity: validity,
		Until:    until,
		Cend:     cend,
	}, nil
}

// Unlock deletes every node record for res carrying token k, regardless
// of expiry, returns how many were removed, and sets T = now.
func (e *Engine) Unlock(res string, k, now int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if res == "" {
		return 0, fmt.Errorf("%w: empty resource", ErrInvalidArgument)
	}
	if k < 1 {
		return 0, fmt.Errorf("%w: token=%d < 1", ErrInvalidArgument, k)
	}
	if now < 0 || now > maxTime {
		return 0, fmt.Errorf("%w: now=%d not in [0,10^15]", ErrInvalidTime, now)
	}
	if now < e.clock {
		return 0, fmt.Errorf("%w: now=%d < T=%d", ErrClockRewind, now, e.clock)
	}
	removed := e.releaseAll(res, k)
	e.clock = now
	return removed, nil
}

// Restart wipes all records of node i and sets its quiet deadline to
// now + MaxTTL: requests arriving before that instant are rejected.
// T is set to now.
func (e *Engine) Restart(i int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if i < 0 || i >= e.n {
		return fmt.Errorf("%w: node=%d not in [0,%d)", ErrInvalidArgument, i, e.n)
	}
	if now < 0 || now > maxTime {
		return fmt.Errorf("%w: now=%d not in [0,10^15]", ErrInvalidTime, now)
	}
	if now < e.clock {
		return fmt.Errorf("%w: now=%d < T=%d", ErrClockRewind, now, e.clock)
	}
	e.nodes[i].records = make(map[string]record)
	e.nodes[i].quiet = now + e.maxTTL
	e.clock = now
	return nil
}

// Count returns how many nodes hold res with token k and expiry > now.
// It is read-only and does not move T.
func (e *Engine) Count(res string, k, now int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if res == "" {
		return 0, fmt.Errorf("%w: empty resource", ErrInvalidArgument)
	}
	if k < 1 {
		return 0, fmt.Errorf("%w: token=%d < 1", ErrInvalidArgument, k)
	}
	if now < 0 || now > maxTime {
		return 0, fmt.Errorf("%w: now=%d not in [0,10^15]", ErrInvalidTime, now)
	}
	if now < e.clock {
		return 0, fmt.Errorf("%w: now=%d < T=%d", ErrClockRewind, now, e.clock)
	}
	count := 0
	for i := range e.nodes {
		if rec, ok := e.nodes[i].records[res]; ok && rec.token == k && rec.expiry > now {
			count++
		}
	}
	return count, nil
}

// Clock returns the current engine watermark T.
func (e *Engine) Clock() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// releaseAll deletes every record for res with token k on all nodes,
// ignoring expiry, and returns the number of deletions. Caller holds mu.
func (e *Engine) releaseAll(res string, k int64) int {
	removed := 0
	for i := range e.nodes {
		if rec, ok := e.nodes[i].records[res]; ok && rec.token == k {
			delete(e.nodes[i].records, res)
			removed++
		}
	}
	return removed
}
