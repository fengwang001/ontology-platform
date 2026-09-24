// Package acc holds the per-group aggregation state and the merge rules
// that make partial aggregates spillable and recombinable.
package acc

import (
	"encoding/binary"
	"errors"
	"math"
)

// State is the partial aggregate of one group. The zero value is the
// identity element: merging it into any state is a no-op.
type State struct {
	Count int64
	Sum   float64
	Min   float64
	Max   float64
}

// ErrMalformed is returned by DecodeState for short buffers.
var ErrMalformed = errors.New("acc: malformed state encoding")

// New returns the state of a single observed value.
func New(v float64) State {
	return State{Count: 1, Sum: v, Min: v, Max: v}
}

// Add folds one more value into the state.
func (s *State) Add(v float64) {
	s.Count++
	s.Sum += v
	s.Min = math.Min(s.Min, v)
	s.Max = math.Max(s.Max, v)
}

// Merge folds another partial state into s. Count/Sum merge additively;
// Min/Max merge by extremum (idempotent). All four are commutative and
// associative (Sum associativity holds exactly only for rounding-free
// arithmetic, e.g. small integers).
func (s *State) Merge(o State) {
	if o.Count == 0 {
		return
	}
	if s.Count == 0 {
		*s = o
		return
	}
	s.Count += o.Count
	s.Sum += o.Sum
	s.Min = math.Min(s.Min, o.Min)
	s.Max = math.Max(s.Max, o.Max)
}

// Encode serializes the state as 32 fixed bytes:
// count(8) | sum(8) | min(8) | max(8), all little-endian.
func (s State) Encode() []byte {
	var b [32]byte
	binary.LittleEndian.PutUint64(b[0:8], uint64(s.Count))
	binary.LittleEndian.PutUint64(b[8:16], math.Float64bits(s.Sum))
	binary.LittleEndian.PutUint64(b[16:24], math.Float64bits(s.Min))
	binary.LittleEndian.PutUint64(b[24:32], math.Float64bits(s.Max))
	return b[:]
}

// DecodeState parses the 32-byte form produced by Encode.
func DecodeState(b []byte) (State, error) {
	if len(b) < 32 {
		return State{}, ErrMalformed
	}
	return State{
		Count: int64(binary.LittleEndian.Uint64(b[0:8])),
		Sum:   math.Float64frombits(binary.LittleEndian.Uint64(b[8:16])),
		Min:   math.Float64frombits(binary.LittleEndian.Uint64(b[16:24])),
		Max:   math.Float64frombits(binary.LittleEndian.Uint64(b[24:32])),
	}, nil
}
