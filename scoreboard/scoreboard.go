// Package scoreboard implements a SACK-based sender scoreboard that tracks
// sent segments, cumulative and selective acknowledgments, loss detection,
// and retransmission under an in-flight byte limit.
//
// Sequence numbers are byte offsets starting at 0. A segment boundary is the
// start of a tracked segment or the sent end. An unacknowledged,
// non-selectively-acknowledged segment is lost iff at least D disjoint
// contiguous selectively-acknowledged fragments lie above it, or at least
// (D-1)*M selectively-acknowledged bytes lie above it. In-flight bytes are
// the total length of unacknowledged, non-selectively-acknowledged segments
// that are not judged lost or have already been retransmitted.
package scoreboard

import (
	"errors"
	"fmt"
	"sync"
)

// Rejection reasons. Ack validation reports the first applicable reason in
// the order the checks are listed below.
var (
	ErrNonPositiveM      = errors.New("scoreboard: maximum segment length M must be positive")
	ErrThresholdTooSmall = errors.New("scoreboard: duplicate threshold D must be at least 2")
	ErrWindowTooSmall    = errors.New("scoreboard: in-flight limit W must be at least M")

	ErrBadSegmentLength = errors.New("scoreboard: segment length must be between 1 and M")

	ErrCumBehind       = errors.New("scoreboard: cumulative point below highest received cumulative point")
	ErrCumBeyondEnd    = errors.New("scoreboard: cumulative point beyond sent end")
	ErrCumNotBoundary  = errors.New("scoreboard: cumulative point not on a segment boundary")
	ErrBlockEmpty      = errors.New("scoreboard: block empty or inverted")
	ErrBlockNotAbove   = errors.New("scoreboard: block start not above cumulative point")
	ErrBlockBeyondEnd  = errors.New("scoreboard: block end beyond sent end")
	ErrBlockNotOnBound = errors.New("scoreboard: block endpoint not on a segment boundary")

	ErrNoRetransmittable = errors.New("scoreboard: no lost segment awaiting retransmission")
	ErrInflightFull      = errors.New("scoreboard: in-flight byte limit would be exceeded")
)

// Block is a half-open selectively acknowledged byte range [Start, End).
type Block struct {
	Start int
	End   int
}

// SegmentState is a snapshot of one tracked segment.
type SegmentState struct {
	Start         int
	End           int
	Sacked        bool
	Lost          bool
	Retransmitted bool
}

type segment struct {
	start         int
	end           int
	sacked        bool
	lost          bool
	retransmitted bool
}

// Scoreboard records sent segments and acknowledgment state. All methods are
// safe for concurrent use; the in-flight byte count always equals the value
// recomputed from the current records by definition.
type Scoreboard struct {
	mu       sync.Mutex
	m        int
	d        int
	w        int
	segs     []segment
	sack     []Block // merged, disjoint, non-adjacent, sorted union of blocks
	cumAck   int
	sentEnd  int
	inflight int
}

// New creates a Scoreboard. M is the maximum segment length, D the duplicate
// threshold (>= 2), W the in-flight byte limit (>= M).
func New(M, D, W int) (*Scoreboard, error) {
	if M <= 0 {
		return nil, fmt.Errorf("%w, got %d", ErrNonPositiveM, M)
	}
	if D < 2 {
		return nil, fmt.Errorf("%w, got %d", ErrThresholdTooSmall, D)
	}
	if W < M {
		return nil, fmt.Errorf("%w, got W=%d M=%d", ErrWindowTooSmall, W, M)
	}
	return &Scoreboard{m: M, d: D, w: W}, nil
}

// Send appends a segment of the given length and returns its start offset.
func (s *Scoreboard) Send(length int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if length < 1 || length > s.m {
		return 0, fmt.Errorf("%w, got %d (M=%d)", ErrBadSegmentLength, length, s.m)
	}
	start := s.sentEnd
	s.segs = append(s.segs, segment{start: start, end: start + length})
	s.sentEnd += length
	s.recompute()
	return start, nil
}

// Ack records a cumulative point cum (all bytes below cum received) and
// selective acknowledgment blocks. Blocks may overlap or abut; their union
// is recorded. The whole call is atomic: on any rejection nothing changes.
func (s *Scoreboard) Ack(cum int, blocks []Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cum < s.cumAck {
		return fmt.Errorf("%w, got %d, have %d", ErrCumBehind, cum, s.cumAck)
	}
	if cum > s.sentEnd {
		return fmt.Errorf("%w, got %d, sent end %d", ErrCumBeyondEnd, cum, s.sentEnd)
	}
	if !s.isBoundary(cum) {
		return fmt.Errorf("%w, got %d", ErrCumNotBoundary, cum)
	}
	for _, b := range blocks {
		if b.Start >= b.End {
			return fmt.Errorf("%w, got [%d,%d)", ErrBlockEmpty, b.Start, b.End)
		}
		if b.Start <= cum {
			return fmt.Errorf("%w, got [%d,%d) with cumulative point %d", ErrBlockNotAbove, b.Start, b.End, cum)
		}
		if b.End > s.sentEnd {
			return fmt.Errorf("%w, got [%d,%d), sent end %d", ErrBlockBeyondEnd, b.Start, b.End, s.sentEnd)
		}
		if !s.isBoundary(b.Start) || !s.isBoundary(b.End) {
			return fmt.Errorf("%w, got [%d,%d)", ErrBlockNotOnBound, b.Start, b.End)
		}
	}
	s.cumAck = cum
	for _, b := range blocks {
		s.addBlock(b)
	}
	s.recompute()
	return nil
}

// Retransmit marks the lost, not-yet-retransmitted segment with the smallest
// start as retransmitted and returns its start. It fails with
// ErrNoRetransmittable when no such segment exists, or with ErrInflightFull
// when the in-flight bytes plus the segment length would exceed W.
func (s *Scoreboard) Retransmit() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := -1
	for i := range s.segs {
		if s.segs[i].lost && !s.segs[i].retransmitted {
			target = i
			break
		}
	}
	if target < 0 {
		return 0, ErrNoRetransmittable
	}
	seg := &s.segs[target]
	if s.inflight+seg.end-seg.start > s.w {
		return 0, fmt.Errorf("%w, in-flight %d + %d > W %d",
			ErrInflightFull, s.inflight, seg.end-seg.start, s.w)
	}
	seg.retransmitted = true
	s.recompute()
	return seg.start, nil
}

// Inflight returns the in-flight byte count: the total length of
// unacknowledged, non-selectively-acknowledged segments that are not judged
// lost or have already been retransmitted.
func (s *Scoreboard) Inflight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight
}

// Snapshot returns the current per-segment state in send order.
func (s *Scoreboard) Snapshot() []SegmentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SegmentState, len(s.segs))
	for i, seg := range s.segs {
		out[i] = SegmentState{
			Start:         seg.start,
			End:           seg.end,
			Sacked:        seg.sacked,
			Lost:          seg.lost,
			Retransmitted: seg.retransmitted,
		}
	}
	return out
}

// CumAck returns the highest received cumulative point.
func (s *Scoreboard) CumAck() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cumAck
}

// SentEnd returns the end offset of the most recently sent segment.
func (s *Scoreboard) SentEnd() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sentEnd
}

// isBoundary reports whether off is a segment boundary: the start of a
// tracked segment or the sent end. Callers must hold s.mu.
func (s *Scoreboard) isBoundary(off int) bool {
	if off == s.sentEnd {
		return true
	}
	for _, seg := range s.segs {
		if seg.start == off {
			return true
		}
	}
	return false
}

// addBlock merges b into the union of selectively acknowledged ranges.
// Callers must hold s.mu.
func (s *Scoreboard) addBlock(b Block) {
	merged := make([]Block, 0, len(s.sack)+1)
	placed := false
	for _, cur := range s.sack {
		switch {
		case cur.End < b.Start:
			merged = append(merged, cur)
		case b.End < cur.Start:
			if !placed {
				merged = append(merged, b)
				placed = true
			}
			merged = append(merged, cur)
		default:
			if cur.Start < b.Start {
				b.Start = cur.Start
			}
			if cur.End > b.End {
				b.End = cur.End
			}
		}
	}
	if !placed {
		merged = append(merged, b)
	}
	s.sack = merged
}

// recompute clears records passed by the cumulative point, refreshes the
// sacked and lost flags, and recomputes the in-flight byte count from its
// definition. Callers must hold s.mu.
func (s *Scoreboard) recompute() {
	kept := s.segs[:0]
	for _, seg := range s.segs {
		if seg.end > s.cumAck {
			kept = append(kept, seg)
		}
	}
	s.segs = kept

	blocks := s.sack[:0]
	for _, b := range s.sack {
		if b.End > s.cumAck {
			blocks = append(blocks, b)
		}
	}
	s.sack = blocks

	for i := range s.segs {
		s.segs[i].sacked = false
		for _, b := range s.sack {
			if b.Start <= s.segs[i].start && s.segs[i].end <= b.End {
				s.segs[i].sacked = true
				break
			}
		}
	}

	for i := range s.segs {
		seg := &s.segs[i]
		seg.lost = false
		if seg.sacked {
			continue
		}
		fragments := 0
		bytes := 0
		for _, b := range s.sack {
			if b.Start >= seg.end {
				fragments++
				bytes += b.End - b.Start
			}
		}
		if fragments >= s.d || bytes >= (s.d-1)*s.m {
			seg.lost = true
		}
	}

	inflight := 0
	for _, seg := range s.segs {
		if !seg.sacked && (!seg.lost || seg.retransmitted) {
			inflight += seg.end - seg.start
		}
	}
	s.inflight = inflight
}
