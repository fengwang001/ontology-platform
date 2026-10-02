// Package redlock provides a deterministic, clock-injected majority lease lock.
package redlock

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig   = errors.New("redlock: invalid configuration")
	ErrInvalidArgument = errors.New("redlock: invalid argument")
	ErrInvalidTime     = errors.New("redlock: invalid time")
	ErrClockMovedBack  = errors.New("redlock: clock moved backwards")
)

// AcquireResult contains the outcome and timing of one acquisition attempt.
type AcquireResult struct {
	Token    int64
	Success  bool
	Granted  int
	ValidFor int64
	Until    int64
	ClockEnd int64
}

// Redlock stores independent node lease tables behind a single serializing lock.
type Redlock struct {
	mu               sync.RWMutex
	nodes            []node
	clock            int64
	tokens           int64
	ttl              int64
	nodeTimeoutValue int64
	driftValue       int64
}

type node struct {
	quietUntil int64
	records    map[string]record
}

type record struct {
	token   int64
	expires int64
}

// New validates configuration and creates N independent lock nodes.
func New(nodeCount, nodeTimeout, driftPermille, maxTTL int64) (*Redlock, error) {
	if nodeCount < 1 || nodeCount > 9 ||
		nodeTimeout < 1 || nodeTimeout > 1_000_000 ||
		driftPermille < 0 || driftPermille > 1000 ||
		maxTTL < 1 || maxTTL > 1_000_000_000 {
		return nil, ErrInvalidConfig
	}

	nodes := make([]node, nodeCount)
	for i := range nodes {
		nodes[i].records = make(map[string]record)
	}

	return &Redlock{
		nodes:            nodes,
		ttl:              maxTTL,
		nodeTimeoutValue: nodeTimeout,
		driftValue:       driftPermille,
	}, nil
}

// Acquire attempts to obtain a lease with the specified deterministic RTT sequence.
func (l *Redlock) Acquire(resource string, ttl, start int64, rtt []int64) (*AcquireResult, error) {
	if resource == "" || ttl < 1 || ttl > l.ttl || len(rtt) != len(l.nodes) {
		return nil, ErrInvalidArgument
	}
	for _, latency := range rtt {
		if latency < -1 || latency > 2*l.nodeTimeoutValue {
			return nil, ErrInvalidArgument
		}
	}
	if start < 0 || start > 1_000_000_000_000_000 {
		return nil, ErrInvalidTime
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if start < l.clock {
		return nil, ErrClockMovedBack
	}

	token := l.tokens + 1
	l.tokens = token

	clientClock := start
	granted := 0
	timeout := l.nodeTimeoutValue

	for i, latency := range rtt {
		if latency == -1 {
			clientClock += timeout
			continue
		}

		arrival := clientClock + latency/2
		grantedHere := l.grant(i, resource, token, arrival, ttl)
		if latency <= timeout {
			clientClock += latency
			if grantedHere {
				granted++
			}
		} else {
			clientClock += timeout
		}
	}

	clockEnd := clientClock
	drift := ttl*l.driftValue/1000 + 2
	validFor := ttl - (clockEnd - start) - drift
	until := start + ttl - drift
	success := granted >= len(l.nodes)/2+1 && validFor > 0

	if !success {
		for i := range l.nodes {
			l.releaseNode(i, resource, token)
		}
	}

	l.clock = clockEnd

	return &AcquireResult{
		Token:    token,
		Success:  success,
		Granted:  granted,
		ValidFor: validFor,
		Until:    until,
		ClockEnd: clockEnd,
	}, nil
}

// Unlock removes matching token records from every node without inspecting expiry.
func (l *Redlock) Unlock(resource string, token, now int64) (int, error) {
	if resource == "" || token < 1 {
		return 0, ErrInvalidArgument
	}
	if !validClockValue(now) {
		return 0, ErrInvalidTime
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if now < l.clock {
		return 0, ErrClockMovedBack
	}

	count := 0
	for i := range l.nodes {
		if l.releaseNode(i, resource, token) {
			count++
		}
	}
	l.clock = now

	return count, nil
}

// Restart clears a node and makes it quiet until now plus MaxTTL.
func (l *Redlock) Restart(node int, now int64) error {
	if node < 0 || node >= len(l.nodes) {
		return ErrInvalidArgument
	}
	if !validClockValue(now) {
		return ErrInvalidTime
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if now < l.clock {
		return ErrClockMovedBack
	}

	l.nodes[node].records = make(map[string]record)
	l.nodes[node].quietUntil = now + l.ttl
	l.clock = now

	return nil
}

// Count returns live matching token records, where expiry must be strictly after now.
func (l *Redlock) Count(resource string, token, now int64) (int, error) {
	if resource == "" || token < 1 {
		return 0, ErrInvalidArgument
	}
	if !validClockValue(now) {
		return 0, ErrInvalidTime
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if now < l.clock {
		return 0, ErrClockMovedBack
	}

	count := 0
	for i := range l.nodes {
		entry, ok := l.nodes[i].records[resource]
		if ok && entry.token == token && entry.expires > now {
			count++
		}
	}

	return count, nil
}

func (l *Redlock) grant(nodeIndex int, resource string, token, arrival, ttl int64) bool {
	target := &l.nodes[nodeIndex]
	if arrival < target.quietUntil {
		return false
	}
	if entry, ok := target.records[resource]; ok && entry.expires > arrival {
		return false
	}
	target.records[resource] = record{
		token:   token,
		expires: arrival + ttl,
	}
	return true
}

func (l *Redlock) releaseNode(nodeIndex int, resource string, token int64) bool {
	target := &l.nodes[nodeIndex]
	entry, ok := target.records[resource]
	if !ok || entry.token != token {
		return false
	}
	delete(target.records, resource)
	return true
}

func validClockValue(value int64) bool {
	return value >= 0 && value <= 1_000_000_000_000_000
}
