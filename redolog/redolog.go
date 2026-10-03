// Package redolog implements a redo log buffer with out-of-order copy
// completion, a contiguous ready watermark, and block-aligned flushing.
package redolog

import (
	"errors"
	"sort"
	"sync"
)

// Error values returned by Buffer operations.
var (
	ErrInvalidParam     = errors.New("redolog: invalid parameter")
	ErrBufferFull       = errors.New("redolog: buffer full")
	ErrIntervalNotFound = errors.New("redolog: interval start not found")
	ErrAlreadyComplete  = errors.New("redolog: interval already completed")
	ErrEndNotFound      = errors.New("redolog: end is not a reserved interval end")
	ErrDuplicateWaiter  = errors.New("redolog: waiter already registered")
)

// Constructor and operation limits.
const (
	maxL0       = 1_000_000_000_000
	maxBlock    = 65_536
	maxCapacity = 1_000_000_000
	maxMx       = 1_000_000
)

type interval struct {
	start     uint64
	end       uint64
	completed bool
}

type waiter struct {
	w   int64
	end uint64
	seq uint64
}

// Buffer is a redo log buffer. All methods are safe for concurrent use and
// behave as if executed in some serial order.
type Buffer struct {
	mu sync.Mutex

	l0       uint64
	block    uint64
	capacity uint64
	mx       uint64

	r     uint64 // reserved LSN frontier
	fd    uint64 // flushed (durable) LSN frontier
	ready uint64 // contiguous completed prefix end

	intervals []interval
	head      int // first interval not yet absorbed into ready
	byStart   map[uint64]int
	ends      map[uint64]struct{}

	waiters  map[int64]waiter
	nextSeq  uint64
	totalBlk uint64
}

// NewBuffer creates a Buffer. l0 must be in [0, 1e12], block in [1, 65536],
// capacity in [1, 1e9] and mx in [1, 1e6].
func NewBuffer(l0, block, capacity, mx uint64) (*Buffer, error) {
	if l0 > maxL0 || block < 1 || block > maxBlock ||
		capacity < 1 || capacity > maxCapacity || mx < 1 || mx > maxMx {
		return nil, ErrInvalidParam
	}
	return &Buffer{
		l0:       l0,
		block:    block,
		capacity: capacity,
		mx:       mx,
		r:        l0,
		fd:       l0,
		ready:    l0,
		byStart:  make(map[uint64]int),
		ends:     make(map[uint64]struct{}),
		waiters:  make(map[int64]waiter),
	}, nil
}

// Reserve reserves the interval [R, R+n) and returns its start and end.
func (b *Buffer) Reserve(n uint64) (start, end uint64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 1 || n > b.capacity {
		return 0, 0, ErrInvalidParam
	}
	if b.r+n-b.fd > b.capacity {
		return 0, 0, ErrBufferFull
	}
	start, end = b.r, b.r+n
	b.byStart[start] = len(b.intervals)
	b.intervals = append(b.intervals, interval{start: start, end: end})
	b.ends[end] = struct{}{}
	b.r = end
	return start, end, nil
}

// Complete marks the interval starting at start as completed.
func (b *Buffer) Complete(start uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx, ok := b.byStart[start]
	if !ok {
		return ErrIntervalNotFound
	}
	if b.intervals[idx].completed {
		return ErrAlreadyComplete
	}
	b.intervals[idx].completed = true
	for b.head < len(b.intervals) && b.intervals[b.head].completed {
		b.ready = b.intervals[b.head].end
		b.head++
	}
	return nil
}

// Wait registers waiter w for end, or reports it already satisfied.
func (b *Buffer) Wait(w int64, end uint64) (satisfied bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if w < 0 {
		return false, ErrInvalidParam
	}
	if _, ok := b.ends[end]; !ok {
		return false, ErrEndNotFound
	}
	if end <= b.fd {
		return true, nil
	}
	if _, ok := b.waiters[w]; ok {
		return false, ErrDuplicateWaiter
	}
	b.waiters[w] = waiter{w: w, end: end, seq: b.nextSeq}
	b.nextSeq++
	return false, nil
}

// Flush flushes up to the ready watermark subject to block alignment and
// the per-flush block limit, waking satisfied waiters.
func (b *Buffer) Flush(force bool) (blocks uint64, fd uint64, woke []int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.ready / b.block * b.block
	if force {
		target = b.ready
	}
	if target <= b.fd {
		return 0, b.fd, nil
	}
	if ceilBlocks(target, b.block)-b.fd/b.block > b.mx {
		target = (b.fd/b.block + b.mx) * b.block
	}
	blocks = ceilBlocks(target, b.block) - b.fd/b.block
	b.fd = target
	b.totalBlk += blocks
	pending := make([]waiter, 0, len(b.waiters))
	for _, wt := range b.waiters {
		if wt.end <= b.fd {
			pending = append(pending, wt)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].end != pending[j].end {
			return pending[i].end < pending[j].end
		}
		return pending[i].seq < pending[j].seq
	})
	for _, wt := range pending {
		delete(b.waiters, wt.w)
		woke = append(woke, wt.w)
	}
	return blocks, b.fd, woke
}

func ceilBlocks(x, block uint64) uint64 {
	return (x + block - 1) / block
}

// R returns the reserved LSN frontier.
func (b *Buffer) R() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.r
}

// Fd returns the flushed LSN frontier.
func (b *Buffer) Fd() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fd
}

// Ready returns the contiguous completed prefix end.
func (b *Buffer) Ready() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ready
}

// TotalBlocks returns the cumulative number of blocks written by Flush.
func (b *Buffer) TotalBlocks() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalBlk
}
