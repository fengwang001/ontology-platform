package history

import "time"

// traverseRequest is a queued traversal. Requests queue up until a drain
// point (explicit Drain or any other kernel operation).
type traverseRequest struct {
	token uint64
	delta int
	at    time.Time
}

// Outcome reports the fate of one traversal request after draining.
// Pos is the resulting position, or -1 when the request did not run.
type Outcome struct {
	Token uint64
	Delta int
	Pos   int
	Err   error
}

// scheduler coalesces traversal requests: when several arrive before a
// drain point, only the newest is applied; the rest are superseded and
// must not change any state.
type scheduler struct {
	pending   []traverseRequest
	nextToken uint64
}

func (s *scheduler) submit(delta int, at time.Time) uint64 {
	s.nextToken++
	s.pending = append(s.pending, traverseRequest{token: s.nextToken, delta: delta, at: at})
	return s.nextToken
}

// drain supersedes all but the newest pending request and applies that
// one. Outcomes are returned in submission order.
func (s *scheduler) drain(apply func(traverseRequest) Outcome) []Outcome {
	if len(s.pending) == 0 {
		return nil
	}
	pending := s.pending
	s.pending = nil
	outcomes := make([]Outcome, 0, len(pending))
	for _, req := range pending[:len(pending)-1] {
		outcomes = append(outcomes, Outcome{Token: req.token, Delta: req.delta, Pos: -1, Err: ErrSuperseded})
	}
	return append(outcomes, apply(pending[len(pending)-1]))
}
