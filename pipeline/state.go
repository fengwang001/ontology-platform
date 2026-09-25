package pipeline

import (
	"ontology/chunker"
	"ontology/resume"
	"ontology/sink"
)

// Stats is a read-only snapshot. Buffered is always Accepted - Confirmed.
type Stats struct {
	Accepted  int64
	Confirmed int64
	Buffered  int64
	Produced  int64
	Closed    bool
}

// Stats returns a consistent snapshot without advancing any state.
func (p *Pipeline) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Accepted:  p.accepted,
		Confirmed: p.confirmed,
		Buffered:  p.accepted - p.confirmed,
		Produced:  p.produced,
		Closed:    p.closed,
	}
}

// ChunkSizes returns a copy of produced data-chunk sizes, in order.
func (p *Pipeline) ChunkSizes() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.sizes))
	copy(out, p.sizes)
	return out
}

// ScanBytes returns the cumulative count of bytes touched while pumping.
func (p *Pipeline) ScanBytes() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scanBytes
}

// Checkpoint captures the full resumable state.
func (p *Pipeline) Checkpoint() resume.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	pend, start, open := p.chk.Snapshot()
	queued := make([]chunker.Chunk, len(p.queue))
	for i, c := range p.queue {
		queued[i] = chunker.Chunk{Data: append([]byte(nil), c.Data...)}
	}
	sizes := make([]int, len(p.sizes))
	copy(sizes, p.sizes)
	return resume.State{
		Queue:     queued,
		FrontOff:  p.frontOff,
		Pend:      pend,
		Start:     start,
		Open:      open,
		Final:     p.final,
		Closed:    p.closed,
		Accepted:  p.accepted,
		Confirmed: p.confirmed,
		Produced:  p.produced,
		Sizes:     sizes,
	}
}

// Restore rebuilds a pipeline from a checkpoint against a fresh sink.
func Restore(st resume.State, snk sink.Sink, clk Clock, cfg Config) (*Pipeline, error) {
	p, err := New(snk, clk, cfg)
	if err != nil {
		return nil, err
	}
	if err := st.Validate(); err != nil {
		return nil, err
	}
	p.queue = make([]chunker.Chunk, len(st.Queue))
	for i, c := range st.Queue {
		p.queue[i] = chunker.Chunk{Data: append([]byte(nil), c.Data...)}
	}
	p.frontOff = st.FrontOff
	p.chk.Restore(st.Pend, st.Start, st.Open)
	p.final = st.Final
	p.closed = st.Closed
	p.accepted = st.Accepted
	p.confirmed = st.Confirmed
	p.produced = st.Produced
	p.sizes = append([]int(nil), st.Sizes...)
	return p, nil
}
