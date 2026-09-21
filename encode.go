package ontology

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Encoding format (canonical, unique per set):
//
//	uvarint  N                     — number of runs
//	N × (byte flag, uvarint length) — flag 0x01 = one-run, 0x00 = zero-run
//
// Because the run list is normalized (alternating values, no zero-length
// runs, no trailing zero run), every distinct set produces exactly one
// possible byte sequence. A run length is a uvarint over uint64, so a
// single run of length 2^32 (the whole universe) encodes directly.
//
// The empty set has N = 0 and encodes as the single byte 0x00; that is
// the deterministic, shortest possible encoding.

// Bytes returns the canonical encoding of the set. Two Bitmaps contain
// the same set if and only if their Bytes are byte-identical, regardless
// of how they were constructed.
func (b *Bitmap) Bytes() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := binary.AppendUvarint(nil, uint64(len(b.runs)))
	for _, r := range b.runs {
		var flag byte
		if r.one {
			flag = 1
		}
		out = append(out, flag)
		out = binary.AppendUvarint(out, r.length)
	}
	return out
}

// Verify checks the three canonical-form invariants and reports the first
// violation found, or nil if the encoding is canonical:
//
//  1. no zero-length runs;
//  2. no adjacent same-value runs (they must have been merged);
//  3. no trailing zero run.
func (b *Bitmap) Verify() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for i, r := range b.runs {
		if r.length == 0 {
			return fmt.Errorf("run %d: zero-length run", i)
		}
		if i > 0 && b.runs[i-1].one == r.one {
			return fmt.Errorf("run %d: adjacent same-value runs not merged", i)
		}
	}
	if n := len(b.runs); n > 0 && !b.runs[n-1].one {
		return errors.New("trailing zero run retained")
	}
	return nil
}
