// Package linetable implements an incrementally encoded bytecode
// line-number table.
//
// Entries (pc, line) are appended in strictly increasing pc order and
// encoded as a byte string of zigzag-mapped, LEB128-encoded deltas.
// The table is safe for concurrent use: every method behaves as if
// executed in some serial order.
package linetable

import (
	"errors"
	"sync"
)

// Distinguishable failure reasons.
var (
	// Construction.
	ErrInvalidSize = errors.New("linetable: instruction count N must be >= 1")

	// Append, checked in this order.
	ErrPCOutOfRange    = errors.New("linetable: pc out of range [0, N)")
	ErrNonPositiveLine = errors.New("linetable: line must be a positive integer")
	ErrPCNotIncreasing = errors.New("linetable: pc not greater than last successfully appended pc")
	ErrFirstPCNotZero  = errors.New("linetable: first appended pc must be 0")

	// Query, checked in this order.
	ErrEmptyTable = errors.New("linetable: table is empty")

	// Varint-level decode errors, reported in byte order.
	ErrVarintTooLong    = errors.New("linetable: varint not terminated within 10 bytes")
	ErrVarintTruncated  = errors.New("linetable: byte stream truncated after continuation bit")
	ErrVarintNonMinimal = errors.New("linetable: varint not in minimal form (trailing zero byte)")

	// Entry-level decode errors, checked in this order per entry.
	ErrFirstPCDeltaNonZero = errors.New("linetable: first pc delta must be 0")
	ErrPCDeltaNotPositive  = errors.New("linetable: non-first pc delta must be positive")
	ErrPCBeyondN           = errors.New("linetable: cumulative pc must be < N")
	ErrLineDeltaZero       = errors.New("linetable: non-first line delta must be non-zero")
	ErrLineNotPositive     = errors.New("linetable: cumulative line must be positive")
)

// maxVarintBytes is the maximum number of bytes in one varint.
const maxVarintBytes = 10

type entry struct {
	pc   int
	line int
}

// Table is a bytecode line-number table for N instructions.
type Table struct {
	mu      sync.RWMutex
	n       int
	entries []entry
	lastPC  int // pc of the last successful Append (including merges)
	hasLast bool
}

// New returns an empty table for n instructions. n must be at least 1.
func New(n int) (*Table, error) {
	if n < 1 {
		return nil, ErrInvalidSize
	}
	return &Table{n: n}, nil
}

// Append records (pc, line). The pc must lie in [0, N), the line must be
// positive, and pc must be strictly greater than the pc of the last
// successful Append; the very first Append must use pc 0. If line equals
// the line of the last entry, the append merges: it succeeds without
// adding an entry and only advances the last-appended pc. A rejected
// Append changes nothing.
func (t *Table) Append(pc, line int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pc < 0 || pc >= t.n {
		return ErrPCOutOfRange
	}
	if line <= 0 {
		return ErrNonPositiveLine
	}
	if !t.hasLast {
		if pc != 0 {
			return ErrFirstPCNotZero
		}
	} else if pc <= t.lastPC {
		return ErrPCNotIncreasing
	}
	if n := len(t.entries); n > 0 && t.entries[n-1].line == line {
		t.lastPC = pc
		t.hasLast = true
		return nil
	}
	t.entries = append(t.entries, entry{pc: pc, line: line})
	t.lastPC = pc
	t.hasLast = true
	return nil
}

// Line returns the source line for pc: the line of the last entry whose
// pc is not greater than pc.
func (t *Table) Line(pc int) (int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.entries) == 0 {
		return 0, ErrEmptyTable
	}
	if pc < 0 || pc >= t.n {
		return 0, ErrPCOutOfRange
	}
	// Binary search for the last entry with entry.pc <= pc. The first
	// entry always has pc 0, so the search never misses.
	lo, hi := 0, len(t.entries)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if t.entries[mid].pc <= pc {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return t.entries[lo].line, nil
}

// Encode serializes all entries as pairs of varints: the zigzag-mapped
// pc delta and line delta against the previous entry, where the first
// entry is measured against (pc 0, line 0). Encode is read-only and
// returns the same bytes for the same table state.
func (t *Table) Encode() []byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var buf []byte
	prevPC, prevLine := 0, 0
	for _, e := range t.entries {
		buf = appendUvarint(buf, zigzag(e.pc-prevPC))
		buf = appendUvarint(buf, zigzag(e.line-prevLine))
		prevPC, prevLine = e.pc, e.line
	}
	return buf
}

// Decode parses an encoded byte string and rebuilds the table for n
// instructions, re-validating every rule. It reports the first error in
// byte order: varint-level errors while scanning, then entry-level
// errors per entry. On any error it fails as a whole and leaves no
// partial state behind. For any valid encoding, re-encoding the decoded
// table reproduces the original bytes.
func Decode(data []byte, n int) (*Table, error) {
	if n < 1 {
		return nil, ErrInvalidSize
	}
	t := &Table{n: n}
	off := 0
	prevPC, prevLine := 0, 0
	first := true
	for off < len(data) {
		pcDelta, err := readUvarint(data, &off)
		if err != nil {
			return nil, err
		}
		lineDelta, err := readUvarint(data, &off)
		if err != nil {
			return nil, err
		}
		pd, ld := unzigzag(pcDelta), unzigzag(lineDelta)
		if first {
			if pd != 0 {
				return nil, ErrFirstPCDeltaNonZero
			}
		} else if pd <= 0 {
			return nil, ErrPCDeltaNotPositive
		}
		pc := prevPC + pd
		if pc >= n {
			return nil, ErrPCBeyondN
		}
		if !first && ld == 0 {
			return nil, ErrLineDeltaZero
		}
		line := prevLine + ld
		if line <= 0 {
			return nil, ErrLineNotPositive
		}
		t.entries = append(t.entries, entry{pc: pc, line: line})
		prevPC, prevLine = pc, line
		first = false
	}
	if m := len(t.entries); m > 0 {
		t.lastPC = t.entries[m-1].pc
		t.hasLast = true
	}
	return t, nil
}

// zigzag maps a signed delta to an unsigned value: n >= 0 becomes 2n,
// n < 0 becomes -2n-1.
func zigzag(n int) uint64 {
	if n >= 0 {
		return uint64(n) * 2
	}
	return uint64(-n)*2 - 1
}

// unzigzag inverts zigzag.
func unzigzag(u uint64) int {
	if u&1 == 0 {
		return int(u >> 1)
	}
	return -int(u>>1) - 1
}

// appendUvarint appends v in LEB128: low 7 bits first, high bit set on
// every byte except the last.
func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// readUvarint reads one LEB128 varint starting at *off and advances *off
// past it.
func readUvarint(data []byte, off *int) (uint64, error) {
	var v uint64
	for i := 0; ; i++ {
		if i >= maxVarintBytes {
			return 0, ErrVarintTooLong
		}
		if *off >= len(data) {
			return 0, ErrVarintTruncated
		}
		b := data[*off]
		*off++
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			if i > 0 && b == 0 {
				return 0, ErrVarintNonMinimal
			}
			return v, nil
		}
	}
}
