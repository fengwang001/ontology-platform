// Package leasearbiter implements a Raft leader read-lease arbiter.
//
// The leader (node 0) tracks heartbeat acks from its followers, derives a
// local read lease from the send timestamps embedded in those acks, falls
// back to ReadIndex-style confirmation when the lease is unavailable, and
// demotes itself when contact with a quorum is lost.
package leasearbiter

import (
	"errors"
	"sync"
)

// Sentinel errors for rejected operations.
var (
	ErrInvalidConfig = errors.New("leasearbiter: invalid configuration")
	ErrInvalidArgs   = errors.New("leasearbiter: invalid arguments")
	ErrClockSkew     = errors.New("leasearbiter: timestamp before clock watermark")
	ErrNotLeader     = errors.New("leasearbiter: not leader")
)

const maxTime = 1_000_000_000_000_000

// ReadResult is the outcome of a Read call.
//
// If Local is true the read may be served locally and LeaseExpiry is the
// lease deadline. Otherwise Pending is true and ReadID is the ReadIndex id.
type ReadResult struct {
	Local       bool
	Pending     bool
	ReadID      int
	LeaseExpiry int64
}

// pendingRead is one queued ReadIndex read.
type pendingRead struct {
	id int
	at int64
}

// Arbiter is the leader read-lease arbiter. Its zero value is not usable;
// construct one with New.
type Arbiter struct {
	mu sync.Mutex

	n     int
	dur   int64
	rho   int
	et    int64
	start int64
	q     int

	ack []int64
	has []bool
	pr  []int64

	pending  []pendingRead
	leader   bool
	t        int64
	nextRead int

	// examined counts pending reads inspected by the most recent accepted
	// Ack. It is reset at the start of every accepted Ack.
	examined int
}

// New constructs an Arbiter for a cluster of n nodes (leader 0 plus
// followers 1..n-1) with lease duration dur, drift per mille rho,
// contact-loss timeout et and initial time start.
func New(n int, dur int64, rho int, et, start int64) (*Arbiter, error) {
	if n < 1 || n > 9 ||
		dur < 1 || dur > 1_000_000_000 ||
		rho < 0 || rho > 999 ||
		et < dur || et > 1_000_000_000 ||
		start < 0 || start > maxTime {
		return nil, ErrInvalidConfig
	}
	a := &Arbiter{
		n:      n,
		dur:    dur,
		rho:    rho,
		et:     et,
		start:  start,
		q:      n/2 + 1,
		ack:    make([]int64, n),
		has:    make([]bool, n),
		pr:     make([]int64, n),
		leader: true,
		t:      start,
	}
	return a, nil
}

// Examined returns the number of pending reads inspected by the most recent
// accepted Ack (capped at the first unconfirmed read).
func (a *Arbiter) Examined() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.examined
}

// leaseWindow is the effective lease duration after clock drift allowance:
// floor(Dur*(1000-Rho)/1000).
func (a *Arbiter) leaseWindow() int64 {
	return a.dur * int64(1000-a.rho) / 1000
}

// leaseBaseLocked returns the (q-1)-th largest follower ack, i.e. the send
// timestamp at which at least q-1 followers had acked. The empty value
// (base, false) means no lease exists yet.
func (a *Arbiter) leaseBaseLocked() (int64, bool) {
	need := a.q - 1
	if need == 0 {
		return 0, true
	}
	sent := make([]int64, 0, a.n-1)
	for i := 1; i < a.n; i++ {
		if a.has[i] {
			sent = append(sent, a.ack[i])
		}
	}
	if len(sent) < need {
		return 0, false
	}
	// Insertion sort descending (at most 8 followers).
	for i := 1; i < len(sent); i++ {
		for j := i; j > 0 && sent[j] > sent[j-1]; j-- {
			sent[j], sent[j-1] = sent[j-1], sent[j]
		}
	}
	return sent[need-1], true
}

// leaseExpiryLocked evaluates the lease at logical time now. With zero
// followers (N==1) the base is now itself.
func (a *Arbiter) leaseExpiryLocked(now int64) (int64, bool) {
	if a.q == 1 {
		return now + a.leaseWindow(), true
	}
	base, ok := a.leaseBaseLocked()
	if !ok {
		return 0, false
	}
	return base + a.leaseWindow(), true
}

// Ack records follower i's heartbeat confirmation: the heartbeat carrying
// the lease was sent at s and the confirmation was received at r. It
// returns the ids of pending reads confirmed in arrival order.
func (a *Arbiter) Ack(i int, s, r int64) ([]int, error) {
	if i < 1 || i > a.n-1 ||
		s < 0 || s > maxTime || r < 0 || r > maxTime ||
		s > r {
		return nil, ErrInvalidArgs
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r < a.t {
		return nil, ErrClockSkew
	}

	if s > a.ack[i] {
		a.ack[i] = s
	}
	a.has[i] = true
	if d := r + a.dur; d > a.pr[i] {
		a.pr[i] = d
	}
	a.t = r

	// Inspect pending reads in arrival order; stop at the first one that
	// cannot yet be confirmed.
	a.examined = 0
	confirmed := []int{}
	keep := 0
	for idx, rd := range a.pending {
		a.examined++
		cnt := 0
		for f := 1; f < a.n; f++ {
			if a.has[f] && a.ack[f] >= rd.at {
				cnt++
			}
		}
		if cnt < a.q-1 {
			break
		}
		confirmed = append(confirmed, rd.id)
		keep = idx + 1
	}
	a.pending = a.pending[keep:]
	return confirmed, nil
}

// Read either serves locally while the lease is valid at now or enqueues a
// ReadIndex read and returns its id.
func (a *Arbiter) Read(now int64) (ReadResult, error) {
	if now < 0 || now > maxTime {
		return ReadResult{}, ErrInvalidArgs
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.t {
		return ReadResult{}, ErrClockSkew
	}
	if !a.leader {
		return ReadResult{}, ErrNotLeader
	}
	a.t = now

	expiry, ok := a.leaseExpiryLocked(now)
	if ok && now < expiry {
		return ReadResult{Local: true, LeaseExpiry: expiry}, nil
	}
	a.nextRead++
	rd := pendingRead{id: a.nextRead, at: now}
	a.pending = append(a.pending, rd)
	return ReadResult{Pending: true, ReadID: rd.id}, nil
}

// Tick advances the clock to now and demotes the leader once the
// start+Et grace period has passed and quorum contact is lost. Pending
// reads aborted by a demotion are returned by id.
func (a *Arbiter) Tick(now int64) (aborted []int, err error) {
	if now < 0 || now > maxTime {
		return nil, ErrInvalidArgs
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.t {
		return nil, ErrClockSkew
	}

	if a.leader && now >= a.start+a.et {
		threshold := now - a.et
		contact := 0
		for f := 1; f < a.n; f++ {
			if a.has[f] && a.ack[f] >= threshold {
				contact++
			}
		}
		if contact+1 < a.q {
			a.leader = false
			aborted = make([]int, len(a.pending))
			for idx, rd := range a.pending {
				aborted[idx] = rd.id
			}
			a.pending = nil
		}
	}
	a.t = now
	return aborted, nil
}

// ChallengerVotes reports how many nodes would vote for a challenger at
// now: followers whose post-ack silence window has elapsed, plus the
// leader's own vote after demotion.
func (a *Arbiter) ChallengerVotes(now int64) (int, error) {
	if now < 0 || now > maxTime {
		return 0, ErrInvalidArgs
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.t {
		return 0, ErrClockSkew
	}
	votes := 0
	for f := 1; f < a.n; f++ {
		if now >= a.pr[f] {
			votes++
		}
	}
	if !a.leader {
		votes++
	}
	return votes, nil
}
