package replay

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

// Result is one range replay run.
type Result struct {
	Events     []event.Event
	Stats      Stats
	IndexStale bool // an anchor was invalid; fell back to a full prefix scan
}

// Range replays the inclusive sequence interval [from, to] with boundary
// semantics: from > to is ErrBadRange; from below the minimum clamps to it;
// to above the maximum replays to the end.
func (l *Log) Range(from, to uint64) (Result, error) {
	if from > to {
		return Result{}, ErrBadRange
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	paths := segmentPaths(l.cfg.Dir)
	res := Result{}
	for _, p := range paths {
		r, err := segment.Open(p)
		if err != nil {
			return res, err
		}
		h := r.Header()
		end := h.Base + h.Count
		if to < h.Base || from >= end || h.Count == 0 {
			r.Close()
			continue
		}
		stale := replayOne(p, r, h, from, to, &res)
		res.Stats.BytesRead += r.BytesRead()
		if stale {
			res.IndexStale = true
		}
		r.Close()
	}
	return res, nil
}

// replayOne scans one segment, appending events within [from,to].
func replayOne(path string, r *segment.Reader, h segment.Header, from, to uint64, res *Result) bool {
	startOff, startOrd, stale := locate(path, r, h, from)
	r.SeekTo(startOff)
	r.SetRecNo(startOrd)
	for {
		ev, _, _, err := r.Next()
		if err != nil {
			_ = errors.Is
			return stale
		}
		if ev.Seq < from {
			res.Stats.Skipped++
			continue
		}
		if ev.Seq > to {
			return stale
		}
		res.Events = append(res.Events, ev)
	}
}

// locate returns the frame offset/ordinal to start scanning from, validating
// the chosen anchor against the real frame at that offset.
func locate(segPath string, r *segment.Reader, h segment.Header, from uint64) (int64, uint64, bool) {
	idx, _ := sparse.ReadFile(filepath.Join(fmt.Sprintf("%s.idx", segPath)))
	if idx != nil {
		if a, ok := idx.Lookup(from); ok && a.Seq >= h.Base {
			if off, ord, valid := anchorValid(r, h, a); valid {
				return off, ord, false
			}
			return segment.HeaderSize, 0, true // stale: full prefix scan
		}
	}
	return segment.HeaderSize, 0, false
}

// anchorValid verifies that offset really starts a frame whose seq matches.
func anchorValid(r *segment.Reader, h segment.Header, a sparse.Anchor) (int64, uint64, bool) {
	r.SeekTo(a.Offset)
	r.SetRecNo(a.Seq - h.Base)
	ev, _, nextOff, err := r.Next()
	if err != nil {
		return 0, 0, false
	}
	if ev.Seq != a.Seq {
		return 0, 0, false
	}
	return nextOff, a.Seq+1-h.Base, true
}
