// Package redolog implements a redo log buffer that lets multiple writers
// reserve LSN ranges up front and finish copying in any order. The flushable
// frontier (Ready) is defined by the longest contiguous completed prefix of
// reserved intervals, and flushes advance the durable LSN (Fd) with
// block-aligned rules while waking commit waiters in a deterministic order.
package redolog

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidParam      = errors.New("redolog: invalid parameter")
	ErrBufferFull        = errors.New("redolog: buffer full")
	ErrIntervalNotFound  = errors.New("redolog: interval not found")
	ErrIntervalCompleted = errors.New("redolog: interval already completed")
	ErrEndNotFound       = errors.New("redolog: end is not the end of any reserved interval")
	ErrWaiterExists      = errors.New("redolog: waiter already registered")
)

const (
	maxL0        = int64(1_000_000_000_000)
	maxBlockSize = int64(65_536)
	maxCapacity  = int64(1_000_000_000)
	maxMaxBlocks = int64(1_000_000)
)

type interval struct {
	start     int64
	end       int64
	completed bool
}

type waiter struct {
	end int64
	seq int64
}

// Buffer is a redo log buffer. All methods are safe for concurrent use and
// behave as if executed in some serial order.
type Buffer struct {
	mu sync.Mutex

	l0   int64
	blks int64
	cap  int64
	mx   int64

	r  int64
	fd int64

	intervals []interval
	byStart   map[int64]int
	ends      map[int64]struct{}
	head      int

	waiters  map[int64]waiter
	nextSeq  int64
	totalBlk int64

	scanned int64
}

// NewBuffer validates the constructor parameters and returns an empty buffer.
func NewBuffer(l0, blockSize, capacity, maxBlocks int64) (*Buffer, error) {
	if l0 < 0 || l0 > maxL0 ||
		blockSize < 1 || blockSize > maxBlockSize ||
		capacity < 1 || capacity > maxCapacity ||
		maxBlocks < 1 || maxBlocks > maxMaxBlocks {
		return nil, ErrInvalidParam
	}
	return &Buffer{
		l0:      l0,
		blks:    blockSize,
		cap:     capacity,
		mx:      maxBlocks,
		r:       l0,
		fd:      l0,
		byStart: make(map[int64]int),
		ends:    make(map[int64]struct{}),
		waiters: make(map[int64]waiter),
	}, nil
}

// Reserve reserves the interval [R, R+n) and returns its start and end.
func (b *Buffer) Reserve(n int64) (start, end int64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 1 || n > b.cap {
		return 0, 0, ErrInvalidParam
	}
	if b.r+n-b.fd > b.cap {
		return 0, 0, ErrBufferFull
	}
	start, end = b.r, b.r+n
	b.intervals = append(b.intervals, interval{start: start, end: end})
	b.byStart[start] = len(b.intervals) - 1
	b.ends[end] = struct{}{}
	b.r = end
	return start, end, nil
}

// Complete marks the interval starting at start as completed.
func (b *Buffer) Complete(start int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx, ok := b.byStart[start]
	if !ok {
		return ErrIntervalNotFound
	}
	if b.intervals[idx].completed {
		return ErrIntervalCompleted
	}
	b.intervals[idx].completed = true
	for b.head < len(b.intervals) && b.intervals[b.head].completed {
		b.head++
		b.scanned++
	}
	return nil
}

// Wait registers waiter w for end, or reports it already satisfied.
func (b *Buffer) Wait(w, end int64) (satisfied bool, err error) {
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
		return false, ErrWaiterExists
	}
	b.waiters[w] = waiter{end: end, seq: b.nextSeq}
	b.nextSeq++
	return false, nil
}

// Flush advances Fd according to the block-aligned rules and wakes waiters.
func (b *Buffer) Flush(force bool) (blocks int64, fd int64, woken []int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	woken = []int64{}
	target := b.readyLocked()
	if !force {
		target = floorDiv(target, b.blks) * b.blks
	}
	if target <= b.fd {
		return 0, b.fd, woken
	}
	if ceilDiv(target, b.blks)-floorDiv(b.fd, b.blks) > b.mx {
		target = (floorDiv(b.fd, b.blks) + b.mx) * b.blks
	}
	blocks = ceilDiv(target, b.blks) - floorDiv(b.fd, b.blks)
	b.fd = target
	b.totalBlk += blocks

	type hit struct {
		w   int64
		end int64
		seq int64
	}
	hits := make([]hit, 0, len(b.waiters))
	for w, wt := range b.waiters {
		if wt.end <= b.fd {
			hits = append(hits, hit{w: w, end: wt.end, seq: wt.seq})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].end != hits[j].end {
			return hits[i].end < hits[j].end
		}
		return hits[i].seq < hits[j].seq
	})
	for _, h := range hits {
		woken = append(woken, h.w)
		delete(b.waiters, h.w)
	}
	return blocks, b.fd, woken
}

// Reserved returns the current reserved frontier R.
func (b *Buffer) Reserved() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.r
}

// Flushed returns the current durable frontier Fd.
func (b *Buffer) Flushed() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fd
}

// Ready returns the current ready frontier.
func (b *Buffer) Ready() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readyLocked()
}

// TotalBlocks returns the accumulated number of written blocks.
func (b *Buffer) TotalBlocks() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalBlk
}

func (b *Buffer) readyLocked() int64 {
	if b.head == 0 {
		return b.l0
	}
	return b.intervals[b.head-1].end
}

func floorDiv(v, d int64) int64 {
	return v / d
}

func ceilDiv(v, d int64) int64 {
	return (v + d - 1) / d
}
