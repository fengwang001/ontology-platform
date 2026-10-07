package netcode

import (
	"fmt"
	"sync"
)

// playerState is the server-side per-player arbitration state.
//
// Invariants:
//   - pending holds consecutive seqs processed+1 .. maxReceived, ascending;
//   - pending is empty iff the player is not in Server.active.
type playerState struct {
	pos         int64
	maxReceived int64
	processed   int64
	pending     []Move
}

// Server is the authoritative arbiter. All methods are safe for concurrent
// use; concurrent calls behave as if executed in some serial order, and
// replaying the same serial call sequence yields the same ack sequence.
//
// Tick only visits players with a non-empty backlog (tracked in active), so
// its cost is proportional to the number of processed inputs and never
// grows with the number of idle players.
type Server struct {
	cfg     Config
	mu      sync.Mutex
	players map[string]*playerState
	active  []string // players with non-empty pending, in activation order
	ticked  bool
	last    int64
}

// NewServer validates cfg and returns an empty server.
func NewServer(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Server{
		cfg:     cfg,
		players: make(map[string]*playerState),
	}, nil
}

// Register adds a player at position 0. Registering the same player twice
// is an error.
func (s *Server) Register(player string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.players[player]; ok {
		return fmt.Errorf("netcode: player %q already registered", player)
	}
	s.players[player] = &playerState{}
	return nil
}

// Receive validates mv and, if accepted, appends it to the player's
// pending backlog without applying it. See Receipt for the domain
// outcomes; hard rejections are returned as *Error.
//
// Rejections are checked in this order and only the first is reported:
// invalid delta > invalid seq > unknown player > duplicate > gap >
// backlog full. A rejected receive changes nothing.
func (s *Server) Receive(player string, mv Move) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mv.Delta == 0 {
		return Receipt{}, &Error{Reason: ReasonInvalidDelta, Player: player, Seq: mv.Seq}
	}
	if mv.Seq < 1 {
		return Receipt{}, &Error{Reason: ReasonInvalidSeq, Player: player, Seq: mv.Seq}
	}
	ps, ok := s.players[player]
	if !ok {
		return Receipt{}, &Error{Reason: ReasonUnknownPlayer, Player: player, Seq: mv.Seq}
	}
	switch {
	case mv.Seq <= ps.maxReceived:
		// Only an accepted receive advances maxReceived, so this seq was
		// accepted before; replay that receipt idempotently.
		return Receipt{Status: StatusDuplicate, Previous: StatusAccepted}, nil
	case mv.Seq > ps.maxReceived+1:
		return Receipt{Status: StatusGap}, nil
	case len(ps.pending) >= s.cfg.P:
		return Receipt{Status: StatusBacklogFull}, nil
	}
	ps.pending = append(ps.pending, mv)
	ps.maxReceived = mv.Seq
	if len(ps.pending) == 1 {
		s.active = append(s.active, player)
	}
	return Receipt{Status: StatusAccepted}, nil
}

// Tick processes up to K pending inputs per player with a non-empty
// backlog and returns one Ack per player that had input processed.
// now must be strictly greater than the previous Tick's now.
//
// Inputs beyond the quota stay in the backlog for the next tick; they are
// never dropped and never overtake earlier inputs. A clock rollback
// rejects the whole tick: nothing is processed and the clock is unchanged.
func (s *Server) Tick(now int64) ([]Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ticked && now <= s.last {
		return nil, &Error{Reason: ReasonClockRollback, Now: now, LastTick: s.last}
	}
	s.ticked = true
	s.last = now

	var acks []Ack
	still := s.active[:0]
	for _, pid := range s.active {
		ps := s.players[pid]
		n := len(ps.pending)
		if n > s.cfg.K {
			n = s.cfg.K
		}
		var rejected []int64
		for i := 0; i < n; i++ {
			mv := ps.pending[i]
			pos, accepted := applyStep(ps.pos, mv.Delta, s.cfg.W, s.cfg.M)
			ps.pos = pos
			if !accepted {
				rejected = append(rejected, mv.Seq)
			}
			ps.processed = mv.Seq
		}
		ps.pending = ps.pending[n:]
		if len(ps.pending) == 0 {
			ps.pending = nil
		} else {
			still = append(still, pid)
		}
		acks = append(acks, Ack{
			Player:       pid,
			ProcessedSeq: ps.processed,
			Position:     ps.pos,
			Rejected:     rejected,
		})
	}
	s.active = still
	return acks, nil
}

// Authoritative returns the authoritative position and processed seq.
func (s *Server) Authoritative(player string) (pos int64, processedSeq int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, ok := s.players[player]
	if !ok {
		return 0, 0, &Error{Reason: ReasonUnknownPlayer, Player: player}
	}
	return ps.pos, ps.processed, nil
}

// PendingLen returns the player's pending backlog length.
func (s *Server) PendingLen(player string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, ok := s.players[player]
	if !ok {
		return 0, &Error{Reason: ReasonUnknownPlayer, Player: player}
	}
	return len(ps.pending), nil
}
