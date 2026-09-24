// Package cbuf is the receiver-side causal delivery buffer.
package cbuf

import (
	"errors"
	"sort"
	"sync"

	"ontology/vc"
)

// ErrBufferFull is returned when a legal non-duplicate message would
// have to enter a full buffer.
var ErrBufferFull = errors.New("cbuf: buffer full")

// Buffer is the receiver state. Use New; the zero value is not usable.
type Buffer struct {
	mu        sync.Mutex
	n, maxBuf int
	local     []int64
	buf       []map[int64]vc.Msg // buf[sender][ownSeq] -> message
	delivered []vc.Msg
	dups      int64
	// checks counts buffered delivery-predicate evaluations during the
	// most recent Receive (cascade included). Unexported, not in any API.
	checks int64
}

// New creates a buffer for n senders with capacity maxBuf.
func New(n, maxBuf int) (*Buffer, error) {
	if n <= 0 || maxBuf < 0 {
		return nil, errors.New("cbuf: illegal parameters")
	}
	b := &Buffer{n: n, maxBuf: maxBuf, local: make([]int64, n), buf: make([]map[int64]vc.Msg, n)}
	for j := range b.buf {
		b.buf[j] = map[int64]vc.Msg{}
	}
	return b, nil
}

func (b *Buffer) count() (c int) {
	for _, m := range b.buf {
		c += len(m)
	}
	return
}

// cascade delivers to a fixpoint, each round the smallest-From
// deliverable message. The candidate is located directly by key
// (j, local[j]+1); there is never a whole-table scan.
func (b *Buffer) cascade() []vc.Msg {
	var out []vc.Msg
	for {
		progress := false
		for j := 0; j < b.n; j++ {
			m, ok := b.buf[j][b.local[j]+1]
			if !ok {
				continue
			}
			b.checks++
			if !vc.Deliverable(m, b.local) {
				continue
			}
			delete(b.buf[j], b.local[j]+1)
			b.local[j]++
			b.delivered = append(b.delivered, m)
			out = append(out, m)
			progress = true
			break // restart at the smallest From
		}
		if !progress {
			return out
		}
	}
}

// Receive processes one arrival: duplicates are dropped, a ready message
// is delivered plus cascade, anything else is buffered (full buffer is
// rejected before any mutation).
func (b *Buffer) Receive(m vc.Msg) ([]vc.Msg, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checks = 0
	if vc.AlreadyDelivered(m, b.local) {
		b.dups++
		return nil, nil
	}
	if _, ok := b.buf[m.From][m.V[m.From]]; ok {
		b.dups++
		return nil, nil
	}
	if !vc.Deliverable(m, b.local) {
		if b.count() >= b.maxBuf {
			return nil, ErrBufferFull
		}
		b.buf[m.From][m.V[m.From]] = m
		return nil, nil
	}
	b.local[m.From]++
	b.delivered = append(b.delivered, m)
	return append([]vc.Msg{m}, b.cascade()...), nil
}

// Delivered returns a copy of the delivery log.
func (b *Buffer) Delivered() []vc.Msg {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]vc.Msg(nil), b.delivered...)
}

// Buffered returns a copy of buffered messages ordered by (From, seq).
func (b *Buffer) Buffered() []vc.Msg {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []vc.Msg
	for j := 0; j < b.n; j++ {
		seqs := make([]int64, 0, len(b.buf[j]))
		for s := range b.buf[j] {
			seqs = append(seqs, s)
		}
		sort.Slice(seqs, func(i, k int) bool { return seqs[i] < seqs[k] })
		for _, s := range seqs {
			out = append(out, b.buf[j][s])
		}
	}
	return out
}

// Local returns a copy of the local vector clock.
func (b *Buffer) Local() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int64(nil), b.local...)
}

// Dups returns the number of arrivals dropped as duplicates.
func (b *Buffer) Dups() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dups
}

// probeCount is an intentionally unexported white-box accessor reached
// via go:linkname by in-module tests/demo; no exported API exposes checks.
func probeCount(b *Buffer) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checks
}
