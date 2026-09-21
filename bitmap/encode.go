package bitmap

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Bytes returns the canonical encoding of the set.
//
// The encoding is a concatenation of runs; each run is one value
// byte (0 or 1) followed by the run length as a uvarint. The empty
// set encodes to a zero-length slice. Because the run list is kept
// in canonical form, equal sets always encode to identical bytes.
func (b *Bitmap) Bytes() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []byte
	for _, r := range b.runs {
		out = append(out, r.val)
		out = binary.AppendUvarint(out, r.length)
	}
	return out
}

// Verify checks the three canonical-form invariants:
//
//  1. no zero-length runs;
//  2. adjacent runs alternate values (no mergeable neighbours);
//  3. no trailing 0-run.
//
// It returns nil when the encoding is canonical.
func (b *Bitmap) Verify() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for i, r := range b.runs {
		if r.length == 0 {
			return fmt.Errorf("run %d: zero length", i)
		}
		if r.val > 1 {
			return fmt.Errorf("run %d: invalid value %d", i, r.val)
		}
		if i > 0 && b.runs[i-1].val == r.val {
			return fmt.Errorf("run %d: adjacent equal-valued runs not merged", i)
		}
	}
	if n := len(b.runs); n > 0 && b.runs[n-1].val == 0 {
		return errors.New("trailing zero run retained")
	}
	return nil
}
