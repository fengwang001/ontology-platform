// Package dvr implements the replay window: eviction of old segments and
// read-only playlist queries over the media timeline.
package dvr

import (
	"errors"
	"sort"
)

// maxPlaylistEnd is the exclusive upper bound of a playlist query range.
const maxPlaylistEnd = int64(1_000_000_000_000_000)

var (
	ErrInvalidParam = errors.New("dvr: invalid parameter")
	ErrNotYet       = errors.New("dvr: no sealed segment yet")
	ErrSlidOut      = errors.New("dvr: range slid out of window")
)

// Segment is one sealed media segment on the stream timeline.
type Segment struct {
	Seq   int64 // stream-wide consecutive sequence number, from 0
	Start int64 // media timeline start (sum of all previously sealed durations)
	Dur   int64 // media duration
	Disc  bool  // discontinuity mark: first segment of an epoch, except seq 0
}

// End returns the exclusive media timeline end of the segment.
func (s Segment) End() int64 { return s.Start + s.Dur }

// Playlist is the result of a range query.
type Playlist struct {
	Segments              []Segment // intersecting segments, ascending Seq
	MediaSequence         int64     // Seq of the first returned segment
	DiscontinuitySequence int64     // count of Disc segments with Seq < MediaSequence
}

// Window keeps the newest sealed segments whose total duration stays at or
// above the configured limit unless a single segment remains.
type Window struct {
	limit    int64
	segs     []Segment // logical window is segs[head:]
	head     int
	total    int64   // total duration of segs[head:]
	sealed   int64   // total segments ever added
	hi       int64   // media end of the newest sealed segment
	discSeqs []int64 // seqs of all Disc segments ever sealed, ascending
	probes   int     // comparisons used to locate the first segment
}

// NewWindow returns an empty window with the given duration limit in ms.
func NewWindow(limit int64) *Window {
	return &Window{limit: limit}
}

// Add seals a segment into the window and evicts old segments.
func (w *Window) Add(seg Segment) {
	w.segs = append(w.segs, seg)
	w.total += seg.Dur
	w.hi = seg.End()
	w.sealed++
	if seg.Disc {
		w.discSeqs = append(w.discSeqs, seg.Seq)
	}
	// Evict while the window stays at or above the limit without the oldest.
	for w.total-w.segs[w.head].Dur >= w.limit {
		w.total -= w.segs[w.head].Dur
		w.head++
	}
	// Compact occasionally so eviction stays amortized O(1).
	if w.head >= 256 && w.head*2 >= len(w.segs) {
		w.segs = append([]Segment(nil), w.segs[w.head:]...)
		w.head = 0
	}
}

// Playlist returns the segments intersecting the half-open range [from, to).
// The range is clipped to the sealed timeline; the query is read-only.
func (w *Window) Playlist(from, to int64) (Playlist, error) {
	if from < 0 || from >= to || to > maxPlaylistEnd {
		return Playlist{}, ErrInvalidParam
	}
	if w.sealed == 0 || from >= w.hi {
		return Playlist{}, ErrNotYet
	}
	if from < w.segs[w.head].Start {
		return Playlist{}, ErrSlidOut
	}
	if to > w.hi {
		to = w.hi
	}
	// Binary search for the last segment with Start <= from; the timeline
	// is contiguous so that segment contains the media instant `from`.
	w.probes = 0
	lo, hi := w.head, len(w.segs)
	for hi-lo > 1 {
		mid := (lo + hi) >> 1
		w.probes++
		if w.segs[mid].Start <= from {
			lo = mid
		} else {
			hi = mid
		}
	}
	// From here on only the returned segments are touched, in order.
	var segs []Segment
	for i := lo; i < len(w.segs) && w.segs[i].Start < to; i++ {
		segs = append(segs, w.segs[i])
	}
	first := segs[0].Seq
	// discSeqs is ascending: count of Disc segments with Seq < first.
	disc := sort.Search(len(w.discSeqs), func(i int) bool {
		return w.discSeqs[i] >= first
	})
	return Playlist{
		Segments:              segs,
		MediaSequence:         first,
		DiscontinuitySequence: int64(disc),
	}, nil
}
