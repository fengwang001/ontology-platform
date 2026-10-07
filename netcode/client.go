package netcode

import "sync"

// Client predicts the local player's position and reconciles against
// authoritative acks. All methods are safe for concurrent use; concurrent
// calls behave as if executed in some serial order.
//
// Memory and per-ack work are O(number of unacked inputs): confirmed
// inputs are dropped from unacked and never touched again, so the cost of
// ApplyAck does not grow with the total input history.
type Client struct {
	cfg    Config
	player string

	mu                sync.Mutex
	nextSeq           int64
	predicted         int64
	lastAuthoritative int64
	lastAcked         int64
	unacked           []Move             // ascending, consecutive seqs
	rejected          map[int64]struct{} // server-rejected seqs > lastAcked
}

// NewClient validates cfg and returns a client for player at position 0.
func NewClient(cfg Config, player string) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Client{
		cfg:      cfg,
		player:   player,
		nextSeq:  1,
		rejected: make(map[int64]struct{}),
	}, nil
}

// Submit assigns the next seq, predicts immediately (step limit + clamp),
// keeps the move as unacked, and returns it for sending to the server.
//
// A move with |delta| > M predicts as rejected (position unchanged) but is
// still kept as unacked, because only the server's ack confirms the
// outcome; the server applies the same rule, so the prediction matches.
func (c *Client) Submit(delta int64) (Move, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if delta == 0 {
		return Move{}, &Error{Reason: ReasonInvalidDelta, Player: c.player}
	}
	mv := Move{Seq: c.nextSeq, Delta: delta}
	c.nextSeq++
	c.unacked = append(c.unacked, mv)
	c.predicted, _ = applyStep(c.predicted, delta, c.cfg.W, c.cfg.M)
	return mv, nil
}

// ApplyAck reconciles against an authoritative ack. It reports whether the
// ack was applied; stale or duplicate acks (ProcessedSeq <= last known)
// are ignored and return false. Acks may arrive out of order, duplicated,
// or lost: applying the same set of acks in any order converges to the
// same predicted position, because only the ack with the max ProcessedSeq
// determines the base and the replay is deterministic.
func (c *Client) ApplyAck(a Ack) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.Player != c.player || a.ProcessedSeq <= c.lastAcked {
		return false
	}
	c.lastAcked = a.ProcessedSeq
	c.lastAuthoritative = a.Position
	// Server-rejected seqs must not be replayed as successes. Our server
	// only reports rejected seqs <= ProcessedSeq (already dropped below),
	// so this is defensive against misbehaving peers; entries are pruned
	// as soon as lastAcked passes them, keeping the set O(unacked).
	for _, seq := range a.Rejected {
		if seq > a.ProcessedSeq {
			c.rejected[seq] = struct{}{}
		}
	}
	keep := 0
	for keep < len(c.unacked) && c.unacked[keep].Seq <= a.ProcessedSeq {
		keep++
	}
	c.unacked = c.unacked[keep:]
	if len(c.unacked) == 0 {
		c.unacked = nil
	}
	for seq := range c.rejected {
		if seq <= c.lastAcked {
			delete(c.rejected, seq)
		}
	}
	c.predicted = replayUnacked(a.Position, c.unacked, c.rejected, c.cfg.W, c.cfg.M)
	return true
}

// Predicted returns the predicted position and the unacked seq list.
func (c *Client) Predicted() (pos int64, unackedSeqs []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seqs := make([]int64, len(c.unacked))
	for i, mv := range c.unacked {
		seqs[i] = mv.Seq
	}
	return c.predicted, seqs
}

// Divergence returns predicted position minus last known authoritative
// position.
func (c *Client) Divergence() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.predicted - c.lastAuthoritative
}

// LastAcked returns the max processed seq the client has accepted.
func (c *Client) LastAcked() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastAcked
}
