// Package replay locates and replays event ranges across segments,
// using the sparse index with a verified-anchor fallback.
package replay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

// ErrRange is returned when from > to.
var ErrRange = errors.New("replay: from greater than to")

// Counters observes the locate/scan work of one Replay call.
type Counters struct {
	Skipped      uint64 // events skipped while scanning from the anchor
	BytesRead    int64  // record bytes read during the scan phase
	IndexInvalid bool   // anchor failed validation; fell back to full scan
}

// Log is an append-only segmented log safe for concurrent use.
type Log struct {
	mu       sync.RWMutex
	dir      string
	every    uint64
	segLimit uint64
	next     uint64
	seg      int
	w        *segment.Writer
}

// Open creates a fresh log in dir (which must not contain segments).
func Open(dir string, every, segLimit uint64) (*Log, error) {
	if every == 0 || segLimit == 0 {
		return nil, errors.New("replay: every and segLimit must be positive")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Log{dir: dir, every: every, segLimit: segLimit}, nil
}

func (l *Log) segPath(i int) string {
	return filepath.Join(l.dir, fmt.Sprintf("seg-%06d.log", i))
}

// Append adds one event with the next sequence number.
func (l *Log) Append(payload []byte) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		w, err := segment.Create(l.segPath(l.seg), l.every, l.next)
		if err != nil {
			return 0, err
		}
		l.w = w
	}
	seq := l.next
	if err := l.w.Append(event.Encode(event.Event{Seq: seq, Payload: payload})); err != nil {
		return 0, err
	}
	l.next++
	if l.w.Header().Count == l.segLimit {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	return seq, nil
}

func (l *Log) rotate() error {
	path := l.segPath(l.seg)
	if err := l.w.Close(); err != nil {
		return err
	}
	l.w = nil
	idx, err := sparse.Build(path)
	if err != nil {
		return err
	}
	err = sparse.WriteFile(path+".idx", idx)
	l.seg++
	return err
}

// Close flushes the current segment.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	if l.w != nil {
		err = l.w.Close()
	}
	return err
}

func (l *Log) segments() ([]string, error) {
	ents, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if len(e.Name()) == len("seg-000000.log") && e.Name()[:4] == "seg-" {
			names = append(names, filepath.Join(l.dir, e.Name()))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Replay returns events with sequence numbers intersecting [from, to].
// from below the minimum sequence clamps to the start; to beyond the
// maximum replays to the end.
func (l *Log) Replay(from, to uint64) ([]event.Event, Counters, error) {
	var c Counters
	if from > to {
		return nil, c, ErrRange
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	segs, err := l.segments()
	if err != nil {
		return nil, c, err
	}
	var out []event.Event
	for _, path := range segs {
		evs, err := l.replaySegment(path, from, to, &c)
		if err != nil {
			return nil, c, err
		}
		out = append(out, evs...)
	}
	return out, c, nil
}

func (l *Log) replaySegment(path string, from, to uint64, c *Counters) ([]event.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hdr, err := segment.ReadHeader(f)
	if err != nil {
		return nil, err
	}
	if hdr.Count == 0 || from >= hdr.FirstSeq+hdr.Count || to < hdr.FirstSeq {
		return nil, nil
	}
	lo := max(from, hdr.FirstSeq)
	hi := min(to, hdr.FirstSeq+hdr.Count-1)
	start := int64(segment.HeaderSize)
	idx, err := sparse.ReadFile(path + ".idx")
	if err != nil {
		if idx, err = sparse.Build(path); err != nil {
			return nil, err
		}
	}
	if a, ok := idx.Locate(lo); ok && validAnchor(f, a) {
		start = a.Offset
	} else if ok {
		c.IndexInvalid = true
	}
	var out []event.Event
	off := start
	for {
		enc, next, err := segment.ReadRecordAt(f, off)
		if err != nil {
			break
		}
		c.BytesRead += next - off
		off = next
		e, err := event.Decode(enc)
		if err != nil {
			return nil, err
		}
		switch {
		case e.Seq < lo:
			c.Skipped++
		case e.Seq <= hi:
			out = append(out, e)
		default:
			return out, nil
		}
	}
	return out, nil
}

func validAnchor(f *os.File, a sparse.Anchor) bool {
	enc, _, err := segment.ReadRecordAt(f, a.Offset)
	e, derr := event.Decode(enc)
	return err == nil && derr == nil && e.Seq == a.Seq
}
