// Package session implements session-consistency token routing:
// reads and writes are routed per session to replicas that are new
// enough, so a session reads its own writes and never goes backward.
package session

import (
	"fmt"
	"log/slog"
	"sync"
)

// Reason identifies why an operation was rejected.
type Reason string

const (
	ReasonUnknownSession Reason = "unknown_session" // session not registered
	ReasonUnknownReplica Reason = "unknown_replica" // replica not registered
	ReasonReplicaLagging Reason = "replica_lagging" // no replica caught up to the token
	ReasonSeqRegression  Reason = "seq_regression"  // advance below current replica progress
	ReasonSeqAhead       Reason = "seq_ahead"       // advance beyond primary sequence
	ReasonEmptyID        Reason = "empty_id"        // empty session or replica id
	ReasonDuplicate      Reason = "duplicate"       // session or replica already registered
)

// Error is a routing error with a distinguishable Reason.
// Rejected operations never mutate router state.
type Error struct {
	Reason Reason
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("session router: %s: %s", e.Reason, e.Detail)
}

// ReadResult describes the outcome of one routed read.
type ReadResult struct {
	Replica  string // chosen replica name
	Progress uint64 // replica applied sequence observed
	Token    uint64 // session token after the read
}

// Router tracks the primary sequence, each replica's applied
// sequence, and each session's token. All methods are safe for
// concurrent use.
type Router struct {
	mu       sync.Mutex
	seq      uint64            // highest committed primary sequence
	replicas map[string]uint64 // replica name -> applied sequence
	sessions map[string]uint64 // session id -> token
	logger   *slog.Logger
}

// NewRouter creates a Router; a nil logger uses slog.Default().
func NewRouter(logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		replicas: make(map[string]uint64),
		sessions: make(map[string]uint64),
		logger:   logger,
	}
}

// RegisterSession registers a session with token 0.
func (r *Router) RegisterSession(id string) error {
	if id == "" {
		return &Error{Reason: ReasonEmptyID, Detail: "session id is empty"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[id]; ok {
		return &Error{Reason: ReasonDuplicate, Detail: fmt.Sprintf("session %q already registered", id)}
	}
	r.sessions[id] = 0
	r.logger.Info("register session", "op", "register_session", "session", id, "token", 0)
	return nil
}

// RegisterReplica registers a read replica with applied sequence 0.
func (r *Router) RegisterReplica(name string) error {
	if name == "" {
		return &Error{Reason: ReasonEmptyID, Detail: "replica name is empty"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.replicas[name]; ok {
		return &Error{Reason: ReasonDuplicate, Detail: fmt.Sprintf("replica %q already registered", name)}
	}
	r.replicas[name] = 0
	r.logger.Info("register replica", "op", "register_replica", "replica", name, "applied", 0)
	return nil
}

// Write commits one write on the primary, returning the new
// sequence, and raises the session token to at least that sequence.
func (r *Router) Write(sessionID string) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.sessions[sessionID]
	if !ok {
		return 0, &Error{Reason: ReasonUnknownSession, Detail: fmt.Sprintf("session %q not registered", sessionID)}
	}
	r.seq++
	if r.seq > token {
		token = r.seq
	}
	r.sessions[sessionID] = token
	r.logger.Info("write",
		"op", "write",
		"session", sessionID,
		"seq", r.seq,
		"token", token,
		"reason", "primary sequence committed; token raised to max(token, seq)",
	)
	return r.seq, nil
}

// Read routes the session to a replica whose applied sequence is at
// least the session token: the least-progress replica wins, ties
// break by lexicographic name. The token then rises to at least the
// observed progress.
func (r *Router) Read(sessionID string) (ReadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.sessions[sessionID]
	if !ok {
		return ReadResult{}, &Error{Reason: ReasonUnknownSession, Detail: fmt.Sprintf("session %q not registered", sessionID)}
	}
	best := ""
	var bestProgress uint64
	for name, applied := range r.replicas {
		if applied < token {
			continue
		}
		if best == "" || applied < bestProgress || (applied == bestProgress && name < best) {
			best, bestProgress = name, applied
		}
	}
	if best == "" {
		r.logger.Info("read rejected",
			"op", "read",
			"session", sessionID,
			"token", token,
			"reason", "no replica applied sequence >= token",
		)
		return ReadResult{}, &Error{
			Reason: ReasonReplicaLagging,
			Detail: fmt.Sprintf("session %q token %d not reached by any replica", sessionID, token),
		}
	}
	if bestProgress > token {
		token = bestProgress
	}
	r.sessions[sessionID] = token
	r.logger.Info("read",
		"op", "read",
		"session", sessionID,
		"token", token,
		"replica", best,
		"progress", bestProgress,
		"reason", "least-progress replica with applied >= token, ties broken by name",
	)
	return ReadResult{Replica: best, Progress: bestProgress, Token: token}, nil
}

// Advance moves a replica's applied sequence to seq. Regression
// (below current progress) or running ahead of the primary is
// rejected.
func (r *Router) Advance(replica string, seq uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.replicas[replica]
	if !ok {
		return &Error{Reason: ReasonUnknownReplica, Detail: fmt.Sprintf("replica %q not registered", replica)}
	}
	if seq < current {
		return &Error{
			Reason: ReasonSeqRegression,
			Detail: fmt.Sprintf("replica %q at %d, cannot regress to %d", replica, current, seq),
		}
	}
	if seq > r.seq {
		return &Error{
			Reason: ReasonSeqAhead,
			Detail: fmt.Sprintf("replica %q cannot advance to %d ahead of primary %d", replica, seq, r.seq),
		}
	}
	r.replicas[replica] = seq
	r.logger.Info("advance",
		"op", "advance",
		"replica", replica,
		"from", current,
		"to", seq,
		"primary", r.seq,
		"reason", "current <= seq <= primary sequence",
	)
	return nil
}
