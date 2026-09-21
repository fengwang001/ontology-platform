package ontology

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Canonical encoding (produced by Bytes):
//
//	The empty set encodes as zero bytes — the shortest possible form.
//	Otherwise the encoding is a sequence of run records in increasing
//	start order. Each record is two unsigned varints:
//	  gap    = run.start - (previous run.start + previous run.length),
//	           or run.start for the first run;
//	  length = run.length (may be up to 2^32, which varint handles).
//
// Because in-memory runs are always normalized (merged, non-empty,
// no trailing zero run), one set has exactly one encoding.
var (
	errZeroLength = errors.New("zero-length run")
	errNotMerged  = errors.New("adjacent or overlapping runs not merged")
	errOverflow   = errors.New("run extends past the 2^32 domain")
)

// Bytes returns the canonical byte encoding of the set.
func (b *Bitmap) Bytes() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var buf []byte
	var tmp [binary.MaxVarintLen64]byte
	prevEnd := uint64(0)
	for _, r := range b.runs {
		n := binary.PutUvarint(tmp[:], uint64(r.start)-prevEnd)
		buf = append(buf, tmp[:n]...)
		n = binary.PutUvarint(tmp[:], r.length)
		buf = append(buf, tmp[:n]...)
		prevEnd = uint64(r.start) + r.length
	}
	return buf
}

// Verify checks the three normalization rules:
//  1. no zero-length runs;
//  2. no mergeable adjacent (or overlapping) same-value runs;
//  3. no trailing zero run — by construction only 1-runs are stored,
//     so this reduces to every run (including the last) having
//     length >= 1 and staying inside the 2^32 domain.
//
// It returns nil when the bitmap is in canonical form.
func (b *Bitmap) Verify() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for i, r := range b.runs {
		if r.length == 0 {
			return fmt.Errorf("run %d: %w", i, errZeroLength)
		}
		if uint64(r.start)+r.length > domainSize {
			return fmt.Errorf("run %d: %w", i, errOverflow)
		}
		if i > 0 && uint64(r.start) <= b.runs[i-1].end() {
			return fmt.Errorf("run %d: %w", i, errNotMerged)
		}
	}
	return nil
}
