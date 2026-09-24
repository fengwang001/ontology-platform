// Package acc holds aggregation state, partial-state merging and state codec.
package acc

import (
	"encoding/binary"
	"errors"
	"math"
)

// State is the partial aggregation state for one group key.
// The zero value is the empty (identity) state.
type State struct {
	Count int64
	Sum   float64
	Min   float64
	Max   float64
}

// Add folds one value into the state. -0.0 is normalized to +0.0.
func (s *State) Add(v float64) {
	if v == 0 {
		v = 0 // normalize -0.0 to +0.0
	}
	if s.Count == 0 {
		s.Min, s.Max = v, v
	} else {
		s.Min = math.Min(s.Min, v)
		s.Max = math.Max(s.Max, v)
	}
	s.Count++
	s.Sum += v
}

// Merge combines two partial states. The empty state (Count == 0) is the
// identity, so Merge is safe for first-segment or residual-empty cases.
func Merge(a, b State) State {
	if a.Count == 0 {
		return b
	}
	if b.Count == 0 {
		return a
	}
	return State{
		Count: a.Count + b.Count,
		Sum:   a.Sum + b.Sum,
		Min:   math.Min(a.Min, b.Min),
		Max:   math.Max(a.Max, b.Max),
	}
}

// ErrShort means the buffer is too short to decode a state entry.
var ErrShort = errors.New("acc: buffer too short")

// Encode serializes one (key, state) entry as
// keyLen u32 | key | count i64 | sum f64 | min f64 | max f64.
func Encode(key string, s State) []byte {
	buf := make([]byte, 4+len(key)+8+24)
	binary.LittleEndian.PutUint32(buf, uint32(len(key)))
	copy(buf[4:], key)
	off := 4 + len(key)
	binary.LittleEndian.PutUint64(buf[off:], uint64(s.Count))
	binary.LittleEndian.PutUint64(buf[off+8:], math.Float64bits(s.Sum))
	binary.LittleEndian.PutUint64(buf[off+16:], math.Float64bits(s.Min))
	binary.LittleEndian.PutUint64(buf[off+24:], math.Float64bits(s.Max))
	return buf
}

// Decode parses a buffer produced by Encode.
func Decode(buf []byte) (string, State, error) {
	if len(buf) < 40 {
		return "", State{}, ErrShort
	}
	n := int(binary.LittleEndian.Uint32(buf))
	if len(buf) < 4+n+32 {
		return "", State{}, ErrShort
	}
	off := 4 + n
	return string(buf[4 : 4+n]), State{
		Count: int64(binary.LittleEndian.Uint64(buf[off:])),
		Sum:   math.Float64frombits(binary.LittleEndian.Uint64(buf[off+8:])),
		Min:   math.Float64frombits(binary.LittleEndian.Uint64(buf[off+16:])),
		Max:   math.Float64frombits(binary.LittleEndian.Uint64(buf[off+24:])),
	}, nil
}
