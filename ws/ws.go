// Package ws decides whether a run of spaces/tabs is trailing. The decision
// is delayed until a newline, an ordinary byte, or end-of-stream arrives.
package ws

import "errors"

// ErrPendingOverflow means the pending trailing-whitespace run exceeded Limit.
var ErrPendingOverflow = errors.New("ws: pending whitespace exceeds limit")

// IsSpace reports whether b is a deletable trailing whitespace byte.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Event is a raw byte/line-ending unit, as produced by an EOL recognizer.
type Event struct {
	Byte    byte
	NewLine bool
	OrigLen int
}

// Decision is a resolved unit. Keep=false units are deleted trailing
// whitespace (OrigLen bytes); kept units carry one byte. NewLine marks a
// normalized line ending (OrigLen is 2 for a \r\n pair).
type Decision struct {
	Byte    byte
	Keep    bool
	NewLine bool
	OrigLen int
}

// Judger buffers an unresolved trailing-space run. Limit<=0 disables the
// limit. A Judger is not safe for concurrent use.
type Judger struct {
	pending []byte
	Limit   int
}

// New returns a Judger with the given pending-byte limit (<=0 = unlimited).
func New(limit int) *Judger { return &Judger{Limit: limit} }

// Push resolves raw events into decisions. It returns ErrPendingOverflow if
// a pending run grows past Limit; out is still valid up to the failure.
func (j *Judger) Push(evts []Event, out []Decision) ([]Decision, error) {
	for _, e := range evts {
		switch {
		case e.NewLine:
			out = j.flush(out, false)
			out = append(out, Decision{Byte: '\n', NewLine: true, OrigLen: e.OrigLen})
		case IsSpace(e.Byte):
			if j.Limit > 0 && len(j.pending) >= j.Limit {
				return out, ErrPendingOverflow
			}
			j.pending = append(j.pending, e.Byte)
		default:
			out = j.flush(out, true)
			out = append(out, Decision{Byte: e.Byte, Keep: true, OrigLen: e.OrigLen})
		}
	}
	return out, nil
}

// Finish resolves a still-pending run at end-of-stream: it is trailing and
// gets deleted.
func (j *Judger) Finish(out []Decision) []Decision { return j.flush(out, false) }

// Pending reports how many whitespace bytes are currently undecided.
func (j *Judger) Pending() int { return len(j.pending) }

func (j *Judger) flush(out []Decision, keep bool) []Decision {
	for _, b := range j.pending {
		out = append(out, Decision{Byte: b, Keep: keep, OrigLen: 1})
	}
	j.pending = j.pending[:0]
	return out
}
