// Package leaseread implements a Raft leader read-lease arbiter.
//
// The arbiter tracks follower heartbeat acknowledgements, derives a local
// read lease from their send timestamps, falls back to ReadIndex-style
// confirmation when the lease is invalid, and uses follower vote-silence
// windows to bound the votes a challenger can gather during a valid lease.
package leaseread

import (
	"errors"
	"sort"
	"sync"
)

// Sentinel errors. Only the first violated rule is reported for each call.
var (
	ErrInvalidConfig = errors.New("leaseread: invalid configuration")
	ErrInvalidArg    = errors.New("leaseread: invalid argument")
	ErrClockRewind   = errors.New("leaseread: clock rewind")
	ErrNotLeader     = errors.New("leaseread: not leader")
)

const maxTime = int64(1_000_000_000_000_000)

// ReadResult is the outcome of a Read call.
//
// Kind is "local" when the read is served from the local lease, or
// "pending" when it is queued for ReadIndex-style confirmation (Id is then
// the assigned read id starting from 1 and Expire is the current lease
// expiry if any).
type ReadResult struct {
	Kind   string
	Id     int64
	Expire int64
}

// Arbiter is a goroutine-safe read-lease arbiter.
type Arbiter struct {
	mu sync.Mutex

	n     int
	dur   int64
	rho   int64
	et    int64
	start int64

	// ack[i] is the latest heartbeat send timestamp acknowledged by
	// follower i (0 means no acknowledgement; followers are 1..n-1).
	ack []int64
	// has[i] records whether follower i has ever acknowledged, so that a
	// legitimate send timestamp of 0 is not confused with "no ack".
	has []bool
	// pr[i] is the follower's vote-silence deadline (r + Dur).
	pr []int64
	// pending is the ReadIndex queue in arrival order.
	pending []pendingRead
	leader  bool
	t       int64

	nextId   int64
	examined int64
}

type pendingRead struct {
	id int64
	a  int64
}

// New validates the whole configuration and creates an arbiter.
func New(n int, dur, rho, et, start int64) (*Arbiter, error) {
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
		ack:    make([]int64, n),
		has:    make([]bool, n),
		pr:     make([]int64, n),
		leader: true,
		t:      start,
		nextId: 1,
	}
	return a, nil
}

// Ack records follower i's acknowledgement sent at s and received at r.
// It returns the ids of pending reads confirmed, in arrival order.
func (a *Arbiter) Ack(i int, s, r int64) ([]int64, error) {
	if i < 1 || i > a.n-1 || s < 0 || s > maxTime || r < 0 || r > maxTime || s > r {
		return nil, ErrInvalidArg
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r < a.t {
		return nil, ErrClockRewind
	}

	if s > a.ack[i] {
		a.ack[i] = s
	}
	a.has[i] = true
	if rd := r + a.dur; rd > a.pr[i] {
		a.pr[i] = rd
	}
	a.t = r

	confirmed := []int64{}
	a.examined = 0
	if a.leader && len(a.pending) > 0 {
		stop := len(a.pending)
		for _, rd := range a.pending {
			a.examined++
			c := 0
			for f := 1; f < a.n; f++ {
				if a.has[f] && a.ack[f] >= rd.a {
					c++
				}
			}
			if c >= a.q()-1 {
				confirmed = append(confirmed, rd.id)
				continue
			}
			stop = int(a.examined - 1)
			break
		}
		a.pending = append([]pendingRead(nil), a.pending[stop:]...)
	}
	return confirmed, nil
}

// Read attempts a local read at now or queues a ReadIndex read.
func (a *Arbiter) Read(now int64) (ReadResult, error) {
	if now < 0 || now > maxTime {
		return ReadResult{}, ErrInvalidArg
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.t {
		return ReadResult{}, ErrClockRewind
	}
	if !a.leader {
		return ReadResult{}, ErrNotLeader
	}

	a.t = now

	if a.n == 1 {
		return ReadResult{Kind: "local", Expire: now + a.leaseLen()}, nil
	}

	if base, ok := a.leaseBaseLocked(now); ok {
		expire := base + a.leaseLen()
		if now < expire {
			return ReadResult{Kind: "local", Expire: expire}, nil
		}
	}

	rd := pendingRead{id: a.nextId, a: now}
	a.nextId++
	a.pending = append(a.pending, rd)
	return ReadResult{Kind: "pending", Id: rd.id}, nil
}

// Tick advances the clock and steps down when quorum contact is lost.
// It returns the ids of pending reads aborted by the step-down.
func (a *Arbiter) Tick(now int64) ([]int64, error) {
	if now < 0 || now > maxTime {
		return nil, ErrInvalidArg
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.t {
		return nil, ErrClockRewind
	}

	aborted := []int64{}
	if a.leader && now >= a.start+a.et {
		threshold := now - a.et
		c := 0
		for f := 1; f < a.n; f++ {
			if a.has[f] && a.ack[f] >= threshold {
				c++
			}
		}
		if c+1 < a.q() {
			a.leader = false
			for _, rd := range a.pending {
				aborted = append(aborted, rd.id)
			}
			a.pending = nil
		}
	}
	a.t = now
	return aborted, nil
}

// ChallengerVotes reports how many nodes would vote for a challenger at now.
func (a *Arbiter) ChallengerVotes(now int64) (int, error) {
	if now < 0 || now > maxTime {
		return 0, ErrInvalidArg
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.t {
		return 0, ErrClockRewind
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

func (a *Arbiter) q() int { return a.n/2 + 1 }

// leaderVotesLocked is a read-only snapshot of leadership and challenger
// votes at now (it does not advance the clock watermark).
func (a *Arbiter) leaderVotesLocked(now int64) (bool, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	votes := 0
	for f := 1; f < a.n; f++ {
		if now >= a.pr[f] {
			votes++
		}
	}
	if !a.leader {
		votes++
	}
	return a.leader, votes
}

func (a *Arbiter) debugExamined() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.examined
}

func (a *Arbiter) leaseLen() int64 { return a.dur * (1000 - a.rho) / 1000 }

// leaseBaseLocked returns the (q-1)-th largest acknowledged send timestamp
// among followers. With q==1 the base is the caller-supplied now.
func (a *Arbiter) leaseBaseLocked(now int64) (int64, bool) {
	need := a.q() - 1
	if need == 0 {
		return now, true
	}
	acks := make([]int64, 0, a.n-1)
	for f := 1; f < a.n; f++ {
		if a.has[f] {
			acks = append(acks, a.ack[f])
		}
	}
	if len(acks) < need {
		return 0, false
	}
	sort.Slice(acks, func(i, j int) bool { return acks[i] > acks[j] })
	return acks[need-1], true
}
