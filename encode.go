package ontology

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Encoding format (canonical, deterministic):
//
// The encoding is a sequence of runs. Each run is two unsigned varints:
//
//	run[0]:        uvarint(start),              uvarint(length)
//	run[i>0]:      uvarint(start - prevEnd - 1), uvarint(length)
//
// where length = end - start + 1. The gap start-prevEnd-1 is always >= 1
// because adjacent runs are merged in canonical form.
//
// A run's length is encoded as a uint64 varint, so a single run covering
// the whole domain ([0, math.MaxUint32], length 2^32) is representable;
// no 32-bit "end-start+1" computation is ever performed.
//
// The empty set encodes to zero bytes: the deterministic, shortest
// possible encoding. Bytes() of an empty set returns a non-nil empty
// slice so it always compares equal to itself and to []byte{}.
//
// Because the run list is canonical (see package doc), equal sets always
// produce byte-identical output regardless of construction path.
func (s *Set) Bytes() []byte {
	return encodeRuns(s.snapshot())
}

func encodeRuns(runs []interval) []byte {
	buf := make([]byte, 0, len(runs)*4)
	var tmp [binary.MaxVarintLen64]byte
	var prevEnd uint32
	for i, r := range runs {
		var delta uint64
		if i == 0 {
			delta = uint64(r.start)
		} else {
			delta = uint64(r.start) - uint64(prevEnd) - 1
		}
		n := binary.PutUvarint(tmp[:], delta)
		buf = append(buf, tmp[:n]...)
		n = binary.PutUvarint(tmp[:], uint64(r.end)-uint64(r.start)+1)
		buf = append(buf, tmp[:n]...)
		prevEnd = r.end
	}
	return buf
}

// Verify checks the three canonical-form invariants of the run list:
//
//  1. no zero-length runs: every run satisfies start <= end;
//  2. adjacent same-value runs are merged: consecutive runs keep a gap
//     of at least one zero bit (run[i+1].start > run[i].end + 1);
//  3. no trailing zero-run: the representation stores only 1-runs, so a
//     trailing zero-run can never be present; this is asserted by the
//     structural invariant that the list contains set-bit runs only.
//
// It returns nil when the encoding is in canonical form.
func (s *Set) Verify() error {
	return verifyRuns(s.snapshot())
}

func verifyRuns(runs []interval) error {
	for i, r := range runs {
		if r.end < r.start {
			return fmt.Errorf("run %d [%d,%d]: zero-length run", i, r.start, r.end)
		}
		if i == 0 {
			continue
		}
		prev := runs[i-1]
		if prev.end == math.MaxUint32 {
			return fmt.Errorf("run %d: unreachable run after domain-max run", i)
		}
		if r.start <= prev.end+1 {
			return fmt.Errorf("run %d [%d,%d]: adjacent/overlapping runs not merged (prev ends at %d)",
				i, r.start, r.end, prev.end)
		}
	}
	return nil
}
