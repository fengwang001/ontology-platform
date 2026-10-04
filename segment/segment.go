// Package segment accumulates groups of pictures into a pending fragment
// and seals it into segments of at least the target duration.
package segment

import "ontology/dvr"

// Segmenter seals groups of pictures into media segments for one stream.
type Segmenter struct {
	d          int64 // target segment duration
	pending    int64 // accumulated duration of the pending fragment
	nextSeq    int64 // next stream-wide segment sequence number
	mediaEnd   int64 // sum of all sealed durations
	epochFirst bool  // next sealed segment is the first of the current epoch
}

// New returns a Segmenter with the given target segment duration in ms.
func New(d int64) *Segmenter {
	return &Segmenter{d: d}
}

// BeginEpoch marks the next sealed segment as the first of a new epoch.
func (s *Segmenter) BeginEpoch() {
	s.epochFirst = true
}

// Push adds one group of pictures; it seals the pending fragment once its
// accumulated duration reaches the target.
func (s *Segmenter) Push(dur int64) (dvr.Segment, bool) {
	s.pending += dur
	if s.pending < s.d {
		return dvr.Segment{}, false
	}
	return s.seal(), true
}

// Flush seals a non-empty pending fragment as a short segment.
func (s *Segmenter) Flush() (dvr.Segment, bool) {
	if s.pending == 0 {
		return dvr.Segment{}, false
	}
	return s.seal(), true
}

// seal closes the pending fragment as one segment on the media timeline.
func (s *Segmenter) seal() dvr.Segment {
	seg := dvr.Segment{
		Seq:   s.nextSeq,
		Start: s.mediaEnd,
		Dur:   s.pending,
		Disc:  s.epochFirst && s.nextSeq != 0,
	}
	s.nextSeq++
	s.mediaEnd += s.pending
	s.pending = 0
	s.epochFirst = false
	return seg
}
