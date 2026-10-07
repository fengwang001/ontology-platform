// Package netcode implements a server-authoritative input pipeline with
// client-side prediction and reconciliation for a discrete 1-D world.
//
// The package is split into three cooperating modules:
//
//   - server arbitration (server.go): receives Move inputs, enqueues them
//     per player, and applies them under a per-tick quota in Tick.
//   - shared rules (rules.go): the single source of truth for how one move
//     is applied (step limit + clamping), used by both sides.
//   - client prediction & reconciliation (client.go, reconcile.go):
//     predicts locally on submit and reconciles against authoritative acks.
package netcode

import "fmt"

// MaxW is the largest allowed world width.
const MaxW = 1_000_000

// Config holds the immutable world parameters shared by server and client.
type Config struct {
	// W is the world width; positions are integers in the closed range [0, W].
	// Must satisfy 1 <= W <= MaxW.
	W int64
	// K is the per-player, per-tick input quota. Must satisfy 1 <= K <= 50.
	K int
	// M is the single-step limit: a move with |delta| > M is rejected.
	// Must satisfy M >= 1.
	M int64
	// P is the per-player pending backlog capacity. Must satisfy P >= 1.
	P int
}

// Validate reports whether the configuration is legal.
func (c Config) Validate() error {
	if c.W < 1 || c.W > MaxW {
		return fmt.Errorf("netcode: W must be in [1, %d], got %d", MaxW, c.W)
	}
	if c.K < 1 || c.K > 50 {
		return fmt.Errorf("netcode: K must be in [1, 50], got %d", c.K)
	}
	if c.M < 1 {
		return fmt.Errorf("netcode: M must be >= 1, got %d", c.M)
	}
	if c.P < 1 {
		return fmt.Errorf("netcode: P must be >= 1, got %d", c.P)
	}
	return nil
}

// Move is a single player input: move by Delta (non-zero) with sequence Seq.
type Move struct {
	Seq   int64
	Delta int64
}

// Reason identifies why an operation was rejected with an *Error.
// Rejections are checked and reported in the order the constants are
// declared: invalid parameters first, then clock rollback.
type Reason int

const (
	// ReasonInvalidDelta: delta == 0.
	ReasonInvalidDelta Reason = iota
	// ReasonInvalidSeq: seq < 1.
	ReasonInvalidSeq
	// ReasonUnknownPlayer: player not registered.
	ReasonUnknownPlayer
	// ReasonClockRollback: Tick now <= previous Tick now.
	ReasonClockRollback
)

func (r Reason) String() string {
	switch r {
	case ReasonInvalidDelta:
		return "invalid delta (zero)"
	case ReasonInvalidSeq:
		return "invalid seq (< 1)"
	case ReasonUnknownPlayer:
		return "unknown player"
	case ReasonClockRollback:
		return "clock rollback"
	}
	return fmt.Sprintf("reason(%d)", int(r))
}

// Error is a hard rejection: the operation had no effect on any state.
type Error struct {
	Reason Reason
	Player string
	Seq    int64
	// Now and LastTick are only set for ReasonClockRollback.
	Now      int64
	LastTick int64
}

func (e *Error) Error() string {
	if e.Reason == ReasonClockRollback {
		return fmt.Sprintf("netcode: %v: now=%d last=%d", e.Reason, e.Now, e.LastTick)
	}
	return fmt.Sprintf("netcode: %v: player=%q seq=%d", e.Reason, e.Player, e.Seq)
}

// Status is the domain outcome of Server.Receive for a syntactically valid
// move. Domain rejections (duplicate / gap / backlog full) are reported
// here, in this check order, and never mutate server state.
type Status int

const (
	// StatusAccepted: the move entered the pending backlog.
	StatusAccepted Status = iota
	// StatusDuplicate: seq <= max received seq; the previous receipt for
	// that seq is returned idempotently in Receipt.Previous and the move
	// is NOT enqueued again.
	StatusDuplicate
	// StatusGap: seq > max received seq + 1; rejected, nothing changes.
	StatusGap
	// StatusBacklogFull: pending backlog reached P; rejected, nothing
	// changes (in particular the received seq does not advance).
	StatusBacklogFull
)

func (s Status) String() string {
	switch s {
	case StatusAccepted:
		return "accepted"
	case StatusDuplicate:
		return "duplicate"
	case StatusGap:
		return "gap"
	case StatusBacklogFull:
		return "backlog full"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// Receipt is the result of Server.Receive.
type Receipt struct {
	Status Status
	// Previous is meaningful only when Status == StatusDuplicate and
	// carries the earlier receipt's status for that seq. Because only an
	// accepted receive advances the max received seq, any duplicate seq
	// was necessarily accepted before, so Previous is always
	// StatusAccepted in practice.
	Previous Status
}

// Ack is emitted by the server after a Tick for every player that had at
// least one input processed during that Tick.
type Ack struct {
	Player string
	// ProcessedSeq is the max seq processed for the player so far
	// (cumulative, not just this tick).
	ProcessedSeq int64
	// Position is the authoritative position right after ProcessedSeq.
	Position int64
	// Rejected lists the seqs rejected (|delta| > M) during this tick,
	// in ascending order. Rejected inputs still consume quota and still
	// advance ProcessedSeq.
	Rejected []int64
}
