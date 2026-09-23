// Package acc holds the partial aggregate state and its merge rules.
package acc

import (
	"encoding/binary"
	"errors"
	"math"
)

// State is a partial aggregate over a set of rows of one group.
// The zero value is the merge identity: Count=0, Sum=0, Min=+Inf, Max=-Inf
// must be set via Zero() before merging.
type State struct {
	Count uint64
	Sum   float64
	Min   float64
	Max   float64
}

// Zero returns the merge identity state.
func Zero() State {
	return State{Count: 0, Sum: 0, Min: math.Inf(1), Max: math.Inf(-1)}
}

// Add folds one value into s.
func (s State) Add(v float64) State {
	if v == 0 {
		v = 0 // normalize -0.0
	}
	s.Count++
	s.Sum += v
	if v < s.Min {
		s.Min = v
	}
	if v > s.Max {
		s.Max = v
	}
	return s
}

// Merge combines two partial states. Count/Sum accumulate (not idempotent);
// Min/Max select extrema (idempotent). Both are associative and commutative.
func (s State) Merge(o State) State {
	return State{
		Count: s.Count + o.Count,
		Sum:   s.Sum + o.Sum,
		Min:   math.Min(s.Min, o.Min),
		Max:   math.Max(s.Max, o.Max),
	}
}

// ErrMalformed is returned by DecodeState on invalid input.
var ErrMalformed = errors.New("acc: malformed state encoding")

// EncodeState serializes a spilled partial state with its key:
// keyLen uint32 LE | key | count uint64 | sum | min | max (8 bytes each).
func EncodeState(key string, s State) []byte {
	buf := make([]byte, 4+len(key)+32)
	binary.LittleEndian.PutUint32(buf, uint32(len(key)))
	copy(buf[4:], key)
	off := 4 + len(key)
	binary.LittleEndian.PutUint64(buf[off:], s.Count)
	binary.LittleEndian.PutUint64(buf[off+8:], math.Float64bits(s.Sum))
	binary.LittleEndian.PutUint64(buf[off+16:], math.Float64bits(s.Min))
	binary.LittleEndian.PutUint64(buf[off+24:], math.Float64bits(s.Max))
	return buf
}

// DecodeState parses data produced by EncodeState.
func DecodeState(data []byte) (string, State, error) {
	if len(data) < 4 {
		return "", State{}, ErrMalformed
	}
	n := int(binary.LittleEndian.Uint32(data))
	if len(data) != 4+n+32 {
		return "", State{}, ErrMalformed
	}
	key := string(data[4 : 4+n])
	off := 4 + n
	s := State{
		Count: binary.LittleEndian.Uint64(data[off:]),
		Sum:   math.Float64frombits(binary.LittleEndian.Uint64(data[off+8:])),
		Min:   math.Float64frombits(binary.LittleEndian.Uint64(data[off+16:])),
		Max:   math.Float64frombits(binary.LittleEndian.Uint64(data[off+24:])),
	}
	return key, s, nil
}
