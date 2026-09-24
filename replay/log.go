// Package replay locates sequence ranges over segmented, append-only logs
// using sparse indexes and replays them without gaps or duplicates.
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

// ErrBadRange means from > to.
var ErrBadRange = errors.New("replay: from > to")

// Config holds log parameters.
type Config struct {
	Dir        string // log directory
	Every      uint64 // sparse index anchor interval N
	SegmentCap int    // max events per segment
}

// Stats records work done during one Range call (must be copied under lock).
type Stats struct {
	Skipped   int64 // events discarded during location/scanning
	BytesRead int64 // segment bytes touched
}

// Log is a directory of roll-over segments guarded by one RWMutex.
type Log struct {
	cfg Config
	mu  sync.RWMutex

	active *segment.Writer
	next   uint64 // next expected sequence number
	idGen  int
}

// Open creates/opens a log in cfg.Dir. An empty directory starts at seq 0.
func Open(cfg Config) (*Log, error) {
	if cfg.Every == 0 {
		cfg.Every = 128
	}
	if cfg.SegmentCap <= 0 {
		cfg.SegmentCap = 1 << 20
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	l := &Log{cfg: cfg}
	if err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Log) load() error {
	paths := segmentPaths(l.cfg.Dir)
	if len(paths) == 0 {
		l.next = 0
		return nil
	}
	last := paths[len(paths)-1]
	r, err := segment.Open(last)
	if err != nil {
		return err
	}
	h := r.Header()
	maxSeq := h.Base + h.Count
	r.Close()
	w, err := segment.OpenAppend(last)
	if err != nil {
		return err
	}
	l.active = w
	l.next = maxSeq
	l.idGen = len(paths)
	return nil
}

// Config returns the log configuration.
func (l *Log) Config() Config { return l.cfg }

// Append adds one event with the next sequence number assigned automatically.
func (l *Log) Append(payload []byte) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil {
		w, err := l.rollLocked(0)
		if err != nil {
			return 0, err
		}
		l.active = w
	}
	if l.active.Count() >= uint64(l.cfg.SegmentCap) {
		if err := l.active.Close(); err != nil {
			return 0, err
		}
		w, err := l.rollLocked(l.next)
		if err != nil {
			return 0, err
		}
	l.active = w
	}
	seq := l.next
	if err := l.active.Append(event.Event{Seq: seq, Payload: append([]byte(nil), payload...)}); err != nil {
		return 0, err
	}
	l.next++
	return seq, nil
}

func (l *Log) rollLocked(base uint64) (*segment.Writer, error) {
	l.idGen++
	path := filepath.Join(l.cfg.Dir, fmt.Sprintf("seg-%04d", l.idGen))
	return segment.Create(path, base)
}

// Flush closes the active segment; later Appends start a fresh one.
func (l *Log) Flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != nil {
		if err := l.active.Close(); err != nil {
			return err
		}
		l.active = nil
	}
	return nil
}

// Close flushes the active segment.
func (l *Log) Close() error { return l.Flush() }

func segmentPaths(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "seg-*"))
	out := matches[:0:0]
	for _, m := range matches {
		if filepath.Ext(m) == "" {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

var _ = sparse.Anchor{}
