// Package replay locates and replays events by sequence-number range over a
// directory of append-only segment files, using sparse indexes for
// positioning and falling back to a full segment scan when an index anchor
// turns out to be invalid.
package replay

import (
	"errors"
	"io"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

// ErrInvalidRange is returned when from > to.
var ErrInvalidRange = errors.New("replay: invalid range (from > to)")

// Report describes how a single replay was served.
type Report struct {
	// Skipped is the number of events scanned past during positioning.
	Skipped int
	// BytesRead is the number of segment bytes actually read.
	BytesRead int
	// IndexInvalid is true when an anchor failed validation and replay
	// fell back to a full segment scan.
	IndexInvalid bool
}

// Replayer serves range replays from a log directory. A Replayer is not
// safe for concurrent Replay calls; create one per goroutine.
type Replayer struct {
	dir      string
	interval uint64
	skipped  int
	bytes    int
	invalid  bool
}

// New returns a Replayer. interval is the anchor interval used when an
// index is absent or must be rebuilt.
func New(dir string, interval uint64) *Replayer {
	return &Replayer{dir: dir, interval: interval}
}

// Skipped reports the positioning-phase skip count of the last Replay.
func (r *Replayer) Skipped() int { return r.skipped }

// BytesRead reports bytes read during the last Replay.
func (r *Replayer) BytesRead() int { return r.bytes }

type countingReader struct {
	r io.Reader
	n *int
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += n
	return n, err
}

// Replay returns events whose sequence numbers fall in [from, to].
//
// Semantics: from > to is an error; from below the smallest sequence is
// clamped to the segment start; to above the largest sequence replays to
// the end; an empty log returns an empty slice with no error.
func (r *Replayer) Replay(from, to uint64) ([]event.Event, Report, error) {
	r.skipped, r.bytes, r.invalid = 0, 0, false
	if from > to {
		return nil, Report{}, ErrInvalidRange
	}
	paths, err := segment.List(r.dir)
	if err != nil {
		return nil, Report{}, err
	}
	var out []event.Event
	for _, p := range paths {
		rd, err := segment.Open(p)
		if err != nil {
			return nil, r.report(), err
		}
		hdr := rd.Hdr
		if hdr.Count == 0 || hdr.End() <= from || hdr.FirstSeq > to {
			rd.Close()
			continue
		}
		startSeq := from
		if startSeq < hdr.FirstSeq {
			startSeq = hdr.FirstSeq
		}
		offset, seq := r.locate(rd, p, startSeq)
		cr := countingReader{r: rd.Section(offset), n: &r.bytes}
		for seq < startSeq && seq < hdr.End() {
			_, n, derr := event.Decode(cr)
			if derr != nil {
				rd.Close()
				return out, r.report(), nil
			}
			offset += uint64(n)
			seq++
		}
		for seq <= to && seq < hdr.End() {
			payload, _, derr := event.Decode(cr)
			if derr != nil {
				break
			}
			out = append(out, event.Event{Seq: seq, Payload: payload})
			seq++
		}
		rd.Close()
	}
	return out, r.report(), nil
}

func (r *Replayer) report() Report {
	return Report{Skipped: r.skipped, BytesRead: r.bytes, IndexInvalid: r.invalid}
}

// locate returns the byte offset and sequence number to start scanning.
func (r *Replayer) locate(rd *segment.Reader, segPath string, startSeq uint64) (uint64, uint64) {
	hdr := rd.Hdr
	offset, seq := uint64(segment.HeaderSize), hdr.FirstSeq
	if idx, err := sparse.Load(sparse.IndexPath(segPath)); err == nil {
		if anchor, ok := idx.Floor(startSeq); ok {
			if r.anchorValid(rd, anchor) {
				offset, seq = anchor.Offset, anchor.Seq
			} else {
				r.invalid = true
			}
		}
	}
	r.skipped += int(startSeq - seq)
	return offset, seq
}

// anchorValid verifies that a record can be decoded at the anchor offset.
func (r *Replayer) anchorValid(rd *segment.Reader, a sparse.Anchor) bool {
	if a.Offset < segment.HeaderSize || a.Offset >= uint64(rd.Size()) {
		return false
	}
	_, _, err := event.Decode(rd.Section(a.Offset))
	return err == nil
}
