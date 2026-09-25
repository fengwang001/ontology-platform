// Package repair detects segment/index corruption, classifies
// truncation, rebuilds indexes, and finds sequence gaps.
package repair

import (
	"errors"
	"fmt"
	"os"

	"ontology/segment"
	"ontology/sparse"
)

// ErrSeqGap marks a sequence discontinuity between adjacent segments.
var ErrSeqGap = errors.New("repair: sequence gap between segments")

// Gap is a missing sequence range [From, To] between two segments.
type Gap struct {
	From uint64
	To   uint64
}

// Classify returns the sentinel-classified defect of the segment at
// segPath, or nil if the segment reads cleanly.
func Classify(segPath string) error {
	_, _, _, err := segment.Scan(segPath, nil)
	return err
}

// RepairSegment truncates segPath to its maximal recoverable prefix
// and rewrites the header count to the number of readable events.
// It returns the header count before and after the repair.
func RepairSegment(segPath string) (before, after uint64, err error) {
	hdr, good, tailOff, scanErr := segment.Scan(segPath, nil)
	if scanErr == nil {
		return hdr.Count, hdr.Count, nil
	}
	if errors.Is(scanErr, segment.ErrHeaderIncomplete) {
		return 0, 0, scanErr
	}
	f, err := os.OpenFile(segPath, os.O_RDWR, 0)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	if err := f.Truncate(tailOff); err != nil {
		return 0, 0, err
	}
	if err := segment.SetCount(f, good); err != nil {
		return 0, 0, err
	}
	return hdr.Count, good, nil
}

// RebuildIndex reconstructs the sparse index from the segment alone
// and writes it next to the segment file.
func RebuildIndex(segPath string) error {
	idx, err := sparse.Build(segPath)
	if err != nil {
		return err
	}
	return sparse.WriteFile(segPath+".idx", idx)
}

// CheckGaps verifies sequence continuity across the given segment
// paths (in order) and reports any missing ranges.
func CheckGaps(segPaths []string) ([]Gap, error) {
	var gaps []Gap
	var prev segment.Header
	for i, p := range segPaths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		hdr, err := segment.ReadHeader(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		if i > 0 {
			want := prev.FirstSeq + prev.Count
			if hdr.FirstSeq != want {
				gaps = append(gaps, Gap{From: want, To: hdr.FirstSeq - 1})
			}
		}
		prev = hdr
	}
	if len(gaps) > 0 {
		return gaps, fmt.Errorf("%w: %v", ErrSeqGap, gaps)
	}
	return nil, nil
}
