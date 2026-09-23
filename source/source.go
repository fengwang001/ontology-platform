// Package source provides an injectable synthetic in-memory data source.
package source

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrInjected is returned when the source is configured to fail mid-stream.
var ErrInjected = errors.New("source: injected failure")

// Raw is one item read from the data source.
type Raw struct {
	Offset int64
	Data   []byte
}

// Config configures a synthetic source.
type Config struct {
	Count     int           // number of records before a natural EOF (0 => immediate EOF)
	KeySeed   int64         // key = offset%KeySeed; <=0 means every record shares one key
	Delay     time.Duration // pause before producing each record (0 = full speed)
	FailAfter int           // >0: fail with ErrInjected right after producing this many records
	BadEvery  int           // >0: every BadEvery-th record is a malformed byte line
}

// Source is a restartable, thread-safe synthetic source.
type Source struct {
	cfg Config

	mu      sync.Mutex
	pos     int64
	emitted int
	blocks  int64
}

// New creates a source at offset zero.
func New(cfg Config) *Source { return &Source{cfg: cfg} }

// Seek moves the next-read position (used by checkpoint recovery).
func (s *Source) Seek(offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pos = offset
	s.emitted = int(offset)
}

// Pos returns the next offset that will be read.
func (s *Source) Pos() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pos
}

// NoteBlock records one back-pressure event (downstream queue was full).
func (s *Source) NoteBlock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocks++
}

// Blocks reports how many times production was blocked by back-pressure.
func (s *Source) Blocks() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blocks
}

// Next returns the next raw record. The bool is false at natural EOF.
func (s *Source) Next() (Raw, bool, error) {
	if s.cfg.Delay > 0 {
		time.Sleep(s.cfg.Delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emitted >= s.cfg.Count {
		return Raw{}, false, nil
	}
	if s.cfg.FailAfter > 0 && s.emitted >= s.cfg.FailAfter {
		return Raw{}, false, ErrInjected
	}
	off := s.pos
	key := int64(0)
	if s.cfg.KeySeed > 0 {
		key = off % s.cfg.KeySeed
	}
	line := fmt.Sprintf("k%d=%d", key, off)
	if s.cfg.BadEvery > 0 && off%int64(s.cfg.BadEvery) == 0 {
		line = "garbage-line-without-separator"
	}
	s.pos++
	s.emitted++
	return Raw{Offset: off, Data: []byte(line)}, true, nil
}
