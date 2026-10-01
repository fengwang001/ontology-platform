package ledger

import (
	"errors"
	"math"
	"sync"
)

const infinityTxn = math.MaxUint64

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrOutOfSpace      = errors.New("out of space")
	ErrBlockNotFound   = errors.New("block not found")
	ErrBlockDiscarded  = errors.New("block discarded")
	ErrBlockDead       = errors.New("block already dead")
	ErrSnapshotExists  = errors.New("snapshot already exists")
	ErrSnapshotMissing = errors.New("snapshot not found")
	ErrNotHeld         = errors.New("snapshot is not held")
	ErrHeld            = errors.New("snapshot is held")
)

type block struct {
	size int64
	b    uint64
	d    uint64
}

type snapshot struct {
	name string
	t    uint64
	hold int
}

type Ledger struct {
	mu       sync.RWMutex
	capacity int64
	cur      uint64
	nextID   int64
	blocks   map[int64]block
	snaps    map[string]snapshot
}

func New(capacity int64) (*Ledger, error) {
	if capacity < 1 || capacity > 1<<50 {
		return nil, ErrInvalidArgument
	}
	return &Ledger{
		capacity: capacity,
		cur:      1,
		nextID:   1,
		blocks:   make(map[int64]block),
		snaps:    make(map[string]snapshot),
	}, nil
}

func (l *Ledger) Alloc(size int64) (int64, error) {
	if size < 1 || size > 1<<40 {
		return 0, ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.usedLocked()+size > l.capacity {
		return 0, ErrOutOfSpace
	}
	id := l.nextID
	l.blocks[id] = block{size: size, b: l.cur, d: infinityTxn}
	l.nextID++
	return id, nil
}

func (l *Ledger) Free(id int64) error {
	if id < 1 {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	blk, ok := l.blocks[id]
	if !ok {
		if id >= l.nextID {
			return ErrBlockNotFound
		}
		return ErrBlockDiscarded
	}
	if blk.d != infinityTxn {
		return ErrBlockDead
	}
	blk.d = l.cur
	l.blocks[id] = blk
	return nil
}

func (l *Ledger) Snapshot(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.snaps[name]; ok {
		return ErrSnapshotExists
	}
	l.snaps[name] = snapshot{name: name, t: l.cur}
	l.cur++
	return nil
}

func (l *Ledger) Destroy(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap, ok := l.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	if snap.hold > 0 {
		return ErrHeld
	}
	delete(l.snaps, name)
	return nil
}

func (l *Ledger) Hold(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap, ok := l.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	snap.hold++
	l.snaps[name] = snap
	return nil
}

func (l *Ledger) Release(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap, ok := l.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	if snap.hold == 0 {
		return ErrNotHeld
	}
	snap.hold--
	l.snaps[name] = snap
	return nil
}

func (l *Ledger) Rollback(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	target, ok := l.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	for _, snap := range l.snaps {
		if snap.t > target.t && snap.hold > 0 {
			return ErrHeld
		}
	}
	for snapName, snap := range l.snaps {
		if snap.t > target.t {
			delete(l.snaps, snapName)
		}
	}
	for id, blk := range l.blocks {
		if blk.b > target.t {
			delete(l.blocks, id)
			continue
		}
		if blk.d > target.t {
			blk.d = infinityTxn
			l.blocks[id] = blk
		}
	}
	return nil
}

func (l *Ledger) Referenced(name string) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	snap, ok := l.snaps[name]
	if !ok {
		return 0, ErrSnapshotMissing
	}
	var total int64
	for _, blk := range l.blocks {
		if blockInSnapshot(blk, snap.t) {
			total += blk.size
		}
	}
	return total, nil
}

func (l *Ledger) Unique(name string) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	target, ok := l.snaps[name]
	if !ok {
		return 0, ErrSnapshotMissing
	}
	var total int64
	for _, blk := range l.blocks {
		if blk.d == infinityTxn || !blockInSnapshot(blk, target.t) {
			continue
		}
		onlyHolder := true
		for _, snap := range l.snaps {
			if snap.name == target.name {
				continue
			}
			if blockInSnapshot(blk, snap.t) {
				onlyHolder = false
				break
			}
		}
		if onlyHolder {
			total += blk.size
		}
	}
	return total, nil
}

func (l *Ledger) Used() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.usedLocked()
}

func (l *Ledger) Cap() int64 {
	return l.capacity
}

func (l *Ledger) CurrentTxn() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cur
}

func (l *Ledger) usedLocked() int64 {
	var total int64
	for _, blk := range l.blocks {
		if blk.d == infinityTxn {
			total += blk.size
			continue
		}
		for _, snap := range l.snaps {
			if blockInSnapshot(blk, snap.t) {
				total += blk.size
				break
			}
		}
	}
	return total
}

func blockInSnapshot(blk block, t uint64) bool {
	return blk.b <= t && t < blk.d
}
