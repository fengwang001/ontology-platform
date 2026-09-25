package pipeline

import (
	"errors"
	"sync"
	"time"

	"ontology/chunker"
	"ontology/sink"
	"ontology/sizeline"
)

var (
	ErrClosed        = errors.New("pipeline: write after close")
	ErrBackpressure  = sink.ErrBackpressure
	ErrChunkTooLarge = errors.New("pipeline: chunk exceeds max size")
	ErrExtTooLong    = errors.New("pipeline: extension length exceeds limit")
	ErrConfig        = errors.New("pipeline: invalid config")
)

// Clock is the injected time source.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to a Clock.
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

// Config configures the pipeline.
type Config struct {
	MinChunk  int
	MaxChunk  int
	Window    time.Duration
	MaxBuffer int
	MaxExtLen int
	Exts      []sizeline.Ext
}

// Pipeline is the streaming write pipeline.
type Pipeline struct {
	mu        sync.Mutex
	snk       sink.Sink
	clk       Clock
	cfg       Config
	chk       *chunker.Chunker
	queue     []chunker.Chunk
	frontOff  int
	frontEnc  []byte
	final     bool
	closed    bool
	accepted  int64
	confirmed int64
	produced  int64
	sizes     []int
	scanBytes int64
}

// New constructs a pipeline.
func New(snk sink.Sink, clk Clock, cfg Config) (*Pipeline, error) {
	if snk == nil || clk == nil {
		return nil, errors.Join(ErrConfig, errors.New("nil sink or clock"))
	}
	if cfg.MaxChunk <= 0 || cfg.MinChunk > cfg.MaxChunk {
		return nil, errors.Join(ErrChunkTooLarge, errors.New("invalid chunk size limits"))
	}
	if cfg.MaxBuffer <= 0 {
		return nil, errors.Join(ErrConfig, errors.New("max buffer must be positive"))
	}
	if cfg.MaxExtLen >= 0 && sizeline.ExtsLen(cfg.Exts) > cfg.MaxExtLen {
		return nil, errors.Join(ErrExtTooLong, errors.New("encoded extensions too long"))
	}
	pol := chunker.Policy{Min: cfg.MinChunk, Max: cfg.MaxChunk, Window: cfg.Window}
	return &Pipeline{snk: snk, clk: clk, cfg: cfg, chk: chunker.New(pol)}, nil
}

// Write appends source bytes. Zero-length writes are swallowed completely and
// never produce a chunk. n == len(b) means the bytes are accepted; a returned
// ErrBackpressure means data is safely buffered and Pump must be retried.
func (p *Pipeline) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, ErrClosed
	}
	if p.accepted-p.confirmed+int64(len(b)) > int64(p.cfg.MaxBuffer) {
		return 0, ErrBackpressure
	}
	p.chk.Feed(b, p.clk.Now())
	p.accepted += int64(len(b))
	if err := p.pullLocked(); err != nil {
		return len(b), err
	}
	return len(b), p.pumpLocked()
}

// Pump drives the time window and pushes buffered bytes downstream.
func (p *Pipeline) Pump() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chk.Tick(p.clk.Now())
	if err := p.pullLocked(); err != nil {
		return err
	}
	return p.pumpLocked()
}

// Close seals remaining bytes and writes the terminating chunk exactly once.
// Repeated calls are idempotent and only retry pending output.
func (p *Pipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.chk.Flush(p.clk.Now())
		if err := p.pullLocked(); err != nil {
			return err
		}
		p.final = true
	}
	return p.pumpLocked()
}

func (p *Pipeline) pullLocked() error {
	for {
		c, ok := p.chk.Next()
		if !ok {
			return nil
		}
		if c.Len() > p.cfg.MaxChunk {
			return ErrChunkTooLarge
		}
		p.chk.Pop()
		p.queue = append(p.queue, c)
		p.produced++
		p.sizes = append(p.sizes, c.Len())
	}
}

func (p *Pipeline) frontSegmentLocked() []byte {
	if p.frontEnc != nil {
		return p.frontEnc
	}
	switch {
	case len(p.queue) > 0:
		c := p.queue[0]
		enc := sizeline.Line(int64(c.Len()), p.cfg.Exts)
		enc = append(enc, c.Data...)
		enc = append(enc, '\r', '\n')
		p.frontEnc = enc
	case p.final:
		p.frontEnc = []byte("0\r\n\r\n")
	default:
		return nil
	}
	p.scanBytes += int64(len(p.frontEnc))
	return p.frontEnc
}

func (p *Pipeline) confirmFrontLocked() {
	if len(p.queue) > 0 {
		p.confirmed += int64(p.queue[0].Len())
		p.queue[0] = chunker.Chunk{}
		p.queue = p.queue[1:]
		if len(p.queue) == 0 {
			p.queue = nil
		}
	} else {
		p.final = false
	}
	p.frontOff = 0
	p.frontEnc = nil
}

func (p *Pipeline) pumpLocked() error {
	for {
		seg := p.frontSegmentLocked()
		if seg == nil {
			return nil
		}
		n, err := p.snk.Write(seg[p.frontOff:])
		p.scanBytes += int64(n)
		if n > 0 {
			p.frontOff += n
		}
		if p.frontOff == len(seg) {
			p.confirmFrontLocked()
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrBackpressure
		}
	}
}
