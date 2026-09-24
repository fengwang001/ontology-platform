package source

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// Raw is one byte record at a monotonically increasing source position.
type Raw struct {
	Pos  int64
	Data []byte
}

// Source produces replayable records from a caller-provided byte slice.
type Source interface {
	Open(nextPos int64) error
	Read(context.Context) (Raw, error)
	Blocked() int64
}

// Config configures the deterministic in-memory source.
type Config struct {
	Records   [][]byte
	Rate      int // records per second; zero means unlimited
	FailAt    int // record ordinal at which Read returns Fail; negative disables
	EndAt     int // number of records before io.EOF; negative means all records
	BlockProbe func()
}

// Memory is deterministic and safe to resume from any committed position.
type Memory struct {
	cfg     Config
	pos     int64
	started bool
	mu      sync.Mutex
	blocked int64
}

var ErrInvalidPosition = errors.New("source: invalid start position")

func New(cfg Config) *Memory {
	if cfg.FailAt == 0 {
		cfg.FailAt = -1
	}
	if cfg.EndAt == 0 {
		cfg.EndAt = len(cfg.Records)
	}
	if cfg.EndAt < 0 || cfg.EndAt > len(cfg.Records) {
		cfg.EndAt = len(cfg.Records)
	}
	return &Memory{cfg: cfg}
}

func (m *Memory) Open(nextPos int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if nextPos < 0 || nextPos > int64(m.cfg.EndAt) {
		return ErrInvalidPosition
	}
	m.pos = nextPos
	m.started = true
	return nil
}

func (m *Memory) Read(ctx context.Context) (Raw, error) {
	if err := ctx.Err(); err != nil {
		return Raw{}, err
	}
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return Raw{}, errors.New("source: not opened")
	}
	pos := m.pos
	end := int64(m.cfg.EndAt)
	fail := int64(m.cfg.FailAt)
	rate := m.cfg.Rate
	m.mu.Unlock()

	if fail >= 0 && pos >= fail {
		return Raw{}, errors.New("source: injected failure")
	}
	if pos >= end {
		return Raw{}, io.EOF
	}
	if rate > 0 {
		wait := time.Second / time.Duration(rate)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Raw{}, ctx.Err()
		case <-timer.C:
		}
	}
	data := m.cfg.Records[pos]
	cp := append([]byte(nil), data...)
	m.mu.Lock()
	m.pos++
	m.mu.Unlock()
	return Raw{Pos: pos, Data: cp}, nil
}

// MarkBlocked records one full-send episode. It is called by the pipeline.
func (m *Memory) MarkBlocked() {
	m.mu.Lock()
	m.blocked++
	probe := m.cfg.BlockProbe
	m.mu.Unlock()
	if probe != nil {
		probe()
	}
}

func (m *Memory) Blocked() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blocked
}
