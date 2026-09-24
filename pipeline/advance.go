package pipeline

import (
	"errors"

	"ontology/resume"
	"ontology/sink"
)

// Stats is a read-only snapshot; querying never advances any state.
type Stats struct {
	Accepted  int64 // output bytes generated (incl. raw pending payload)
	Confirmed int64 // output bytes confirmed by the sink
	Pending   int64 // buffered unconfirmed bytes (encoded + raw pending)
	Chunks    int64 // data chunks produced
	Closed    bool  // terminating chunk emitted
}

// Advance performs one write attempt of the head buffered piece. It scans at
// most one piece, so its cost never grows with the total buffered size.
func (p *Pipeline) Advance() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.broken {
		return 0, sink.ErrDisconnected
	}
	front := p.out.front()
	if len(front) == 0 {
		return 0, nil
	}
	p.scanBytes += int64(len(front))
	n, err := p.snk.Write(front)
	if n > len(front) {
		n = len(front)
	}
	if n > 0 {
		p.out.confirm(n)
		p.confirmed += int64(n)
	}
	if errors.Is(err, sink.ErrDisconnected) {
		p.broken = true
	}
	return n, err
}

// Stats returns a consistent snapshot under the lock.
func (p *Pipeline) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Accepted:  p.accepted,
		Confirmed: p.confirmed,
		Pending:   p.out.len() + int64(len(p.pending)),
		Chunks:    p.chunks,
		Closed:    p.closed,
	}
}

// ScanBytes reports the diagnostic counter of bytes scanned while advancing.
func (p *Pipeline) ScanBytes() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scanBytes
}

// ChunkSizes returns the payload sizes of all data chunks produced so far.
func (p *Pipeline) ChunkSizes() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.sizes))
	copy(out, p.sizes)
	return out
}

// Checkpoint captures the resumable state; the pipeline stays usable.
func (p *Pipeline) Checkpoint() resume.Checkpoint {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw := make([]byte, len(p.pending))
	copy(raw, p.pending)
	return resume.Checkpoint{
		Accepted:   p.accepted,
		Confirmed:  p.confirmed,
		Chunks:     p.chunks,
		Closed:     p.closed,
		Chunker:    p.ch.Export(),
		Backlog:    p.out.snapshot(),
		RawPending: raw,
		Sizes:      append([]int(nil), p.sizes...),
	}
}
