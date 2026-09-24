// Package pipeline streams upstream bytes to a sink as encoded chunks with
// backpressure handling, checkpointing, and resume.
package pipeline

import (
	"errors"
	"sync"
	"time"

	"ontology/chunker"
	"ontology/resume"
	"ontology/sink"
	"ontology/sizeline"
)

var (
	// ErrClosed is returned by Write after Close.
	ErrClosed = errors.New("pipeline: write after close")
	// ErrWriteTooLarge rejects a single Write over MaxWriteBytes.
	ErrWriteTooLarge = errors.New("pipeline: write exceeds max write bytes")
	// ErrWouldBlock reports backpressure: the buffer is full, retry later.
	ErrWouldBlock = errors.New("pipeline: buffer full, backpressure")
	// ErrExtsTooLarge rejects extensions over MaxExtBytes.
	ErrExtsTooLarge = errors.New("pipeline: extensions exceed max ext bytes")
)

// Config tunes a Pipeline. All limits are required to be positive.
type Config struct {
	MinChunk       int           // aggregate at least this many payload bytes
	MaxChunk       int           // never emit a larger chunk payload
	Window         time.Duration // aggregation time window (injected clock)
	MaxWriteBytes  int           // hard limit for one Write call
	MaxBufferBytes int64         // hard limit for buffered unconfirmed bytes
	MaxExtBytes    int           // hard limit for encoded extensions length
	Exts           []sizeline.Ext
	Clock          chunker.Clock
}

// Pipeline encodes accepted bytes into chunks and writes them downstream.
// All byte counters are in output-stream bytes, so the invariant
// Accepted == Confirmed + Pending holds at every instant.
type Pipeline struct {
	mu        sync.Mutex
	cfg       Config
	ch        *chunker.Chunker
	snk       sink.Sink
	out       outBuf
	pending   []byte // raw payload of the still-open chunk
	accepted  int64
	confirmed int64
	chunks    int64
	sizes     []int
	closed    bool
	broken    bool
	scanBytes int64
}

func validate(cfg Config) error {
	if cfg.Clock == nil || cfg.MinChunk < 1 || cfg.MaxChunk < cfg.MinChunk ||
		cfg.Window < 0 || cfg.MaxWriteBytes < 1 || cfg.MaxBufferBytes < 1 {
		return errors.New("pipeline: invalid configuration")
	}
	if sizeline.ExtsLen(cfg.Exts) > cfg.MaxExtBytes {
		return ErrExtsTooLarge
	}
	return nil
}

// New builds a Pipeline writing to snk.
func New(cfg Config, snk sink.Sink) (*Pipeline, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	ch, err := chunker.New(cfg.MinChunk, cfg.MaxChunk, cfg.Window, cfg.Clock)
	if err != nil {
		return nil, err
	}
	return &Pipeline{cfg: cfg, ch: ch, snk: snk}, nil
}

// Resume rebuilds a Pipeline from a checkpoint, continuing the exact same
// output stream at the first unconfirmed byte.
func Resume(cfg Config, snk sink.Sink, cp resume.Checkpoint) (*Pipeline, error) {
	p, err := New(cfg, snk)
	if err != nil {
		return nil, err
	}
	p.ch.Import(cp.Chunker)
	for _, piece := range cp.Backlog {
		p.out.push(piece)
	}
	p.pending = append(p.pending, cp.RawPending...)
	p.accepted = cp.Accepted
	p.confirmed = cp.Confirmed
	p.chunks = cp.Chunks
	p.sizes = append(p.sizes, cp.Sizes...)
	p.closed = cp.Closed
	return p, nil
}

// encodeChunk builds one output piece: size line, payload, CRLF.
func (p *Pipeline) encodeChunk(payload []byte) []byte {
	head := sizeline.Encode(uint64(len(payload)), p.cfg.Exts)
	piece := make([]byte, 0, len(head)+len(payload)+2)
	piece = append(piece, head...)
	piece = append(piece, payload...)
	return append(piece, '\r', '\n')
}

// Write accepts p. Zero-length writes are swallowed: they never produce a
// chunk, because a zero-size chunk is the end-of-stream marker.
func (p *Pipeline) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, ErrClosed
	}
	if len(b) > p.cfg.MaxWriteBytes {
		return 0, ErrWriteTooLarge
	}
	st := p.ch.Export()
	sizes := p.ch.Feed(b)
	joined := make([]byte, 0, len(p.pending)+len(b))
	joined = append(joined, p.pending...)
	joined = append(joined, b...)
	var pieces [][]byte
	var add int64
	off := 0
	for _, n := range sizes {
		piece := p.encodeChunk(joined[off : off+n])
		off += n
		pieces = append(pieces, piece)
		add += int64(len(piece))
	}
	rest := joined[off:]
	add += int64(len(rest)) - int64(len(p.pending))
	if p.out.len()+int64(len(p.pending))+add > p.cfg.MaxBufferBytes {
		p.ch.Import(st) // rejection must not change any buffered state
		return 0, ErrWouldBlock
	}
	for _, piece := range pieces {
		p.out.push(piece)
	}
	p.pending = rest
	p.accepted += add
	p.chunks += int64(len(sizes))
	p.sizes = append(p.sizes, sizes...)
	return len(b), nil
}

var endMarker = []byte("0\r\n\r\n")

// Close flushes the open chunk, emits the terminating zero-size chunk once,
// and is idempotent.
func (p *Pipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if n, ok := p.ch.Flush(); ok {
		piece := p.encodeChunk(p.pending[:n])
		p.out.push(piece)
		p.accepted += int64(len(piece)) - int64(len(p.pending))
		p.pending = nil
		p.chunks++
		p.sizes = append(p.sizes, n)
	}
	p.out.push(endMarker)
	p.accepted += int64(len(endMarker))
	return nil
}
