// Package mask defines the mask strength order and the pure mask functions.
package mask

import (
	"fmt"
	"hash/fnv"
	"strconv"

	"ontology/policy"
)

// Strength levels.
const (
	Plain   = 0
	Partial = 1
	Hashed  = 2
	Nulled  = 3
	Denied  = 4
)

// Apply maps a plaintext cell through the mask function at level for colType.
// A NULL cell stays NULL at every level. Level Denied panics: the access
// layer must reject the column before masking.
func Apply(cell policy.Cell, colType policy.ColType, level int) policy.Cell {
	if cell == nil {
		return nil
	}
	switch level {
	case Plain:
		return cell
	case Partial:
		return partial(cell, colType)
	case Hashed:
		var text []byte
		if s, ok := cell.(string); ok {
			text = []byte(s)
		} else {
			text = []byte(strconv.FormatInt(cell.(int64), 10))
		}
		h := fnv.New64a()
		h.Write(text)
		return fmt.Sprintf("%016x", h.Sum64())
	case Nulled:
		return nil
	default:
		panic("mask: Apply called on denied level")
	}
}

func partial(cell policy.Cell, colType policy.ColType) policy.Cell {
	if colType == policy.TypeInt {
		v := cell.(int64)
		// Round toward negative infinity to a multiple of 100: Go's /
		// truncates toward zero, so step the quotient down for negative
		// values with a nonzero remainder (-250 -> -300).
		q := v / 100
		if v < 0 && v%100 != 0 {
			q--
		}
		return q * 100
	}
	b := []byte(cell.(string))
	if len(b) <= 4 {
		return string(bytesRepeat('*', len(b)))
	}
	masked := bytesRepeat('*', len(b)-4)
	return string(append(masked, b[len(b)-4:]...))
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
