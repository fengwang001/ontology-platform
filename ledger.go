package ontology

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrFileNotFound    = errors.New("file not found")
	ErrNoDelayedBlocks = errors.New("no delayed blocks")
	ErrFileTooLarge    = errors.New("file length exceeded")
	ErrNoSpace         = errors.New("not enough space")
)

type fileState struct {
	allocated int64
	delayed   int64
	dirtySeq  int64
}

type Ledger struct {
	mu      sync.Mutex
	total   int64
	special int64
	extent  int64
	water   int64

	files   map[int64]fileState
	counter int64
}

// NewLedger creates a space ledger. totalBlocks is T, reservedBlocks is S,
// blocksPerIndexBlock is E, and dirtyWatermark is W.
func NewLedger(totalBlocks, reservedBlocks, blocksPerIndexBlock, dirtyWatermark int64) (*Ledger, error) {
	if totalBlocks < 1 ||
		reservedBlocks < 0 || reservedBlocks > totalBlocks ||
		blocksPerIndexBlock < 1 ||
		dirtyWatermark < 0 {
		return nil, ErrInvalidArgument
	}

	return &Ledger{
		total:   totalBlocks,
		special: reservedBlocks,
		extent:  blocksPerIndexBlock,
		water:   dirtyWatermark,
		files:   make(map[int64]fileState),
	}, nil
}

// Write appends delayed blocks and returns file IDs flushed by writeback.
func (l *Ledger) Write(fileID, blockCount int64, privileged bool) ([]int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if fileID < 0 || blockCount <= 0 {
		return nil, ErrInvalidArgument
	}

	current := l.files[fileID]
	currentTotal := current.allocated + current.delayed
	nextTotal, ok := addNonNegative(currentTotal, blockCount)
	if !ok {
		return nil, ErrNoSpace
	}

	required := blockCount + indexBlocks(nextTotal, l.extent) - indexBlocks(currentTotal, l.extent)
	if required < 0 {
		return nil, ErrInvalidArgument
	}

	free := l.freeLocked()
	reserved := l.reservedLocked()
	limit := free - reserved
	if !privileged {
		limit -= l.special
	}
	if limit < 0 || required > limit {
		return nil, ErrNoSpace
	}

	l.counter++
	current.delayed += blockCount
	if current.dirtySeq == 0 {
		current.dirtySeq = l.counter
	}
	l.files[fileID] = current

	flushed := make([]int64, 0)
	for l.delayedTotalLocked() > l.water {
		oldestID, ok := l.oldestDirtyLocked()
		if !ok {
			break
		}
		l.flushLocked(oldestID)
		flushed = append(flushed, oldestID)
	}

	return flushed, nil
}

// Flush allocates all delayed blocks for a file without a space check.
func (l *Ledger) Flush(fileID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if fileID < 0 {
		return ErrInvalidArgument
	}
	current, ok := l.files[fileID]
	if !ok {
		return ErrFileNotFound
	}
	if current.delayed == 0 {
		return ErrNoDelayedBlocks
	}

	l.flushLocked(fileID)
	return nil
}

// Truncate removes blocks from the end, consuming delayed blocks first.
func (l *Ledger) Truncate(fileID, blockCount int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if fileID < 0 || blockCount <= 0 {
		return ErrInvalidArgument
	}
	current, ok := l.files[fileID]
	if !ok {
		return ErrFileNotFound
	}
	if blockCount > current.allocated+current.delayed {
		return ErrFileTooLarge
	}

	fromDelayed := minInt64(blockCount, current.delayed)
	current.delayed -= fromDelayed
	blockCount -= fromDelayed
	current.allocated -= blockCount
	if current.delayed == 0 {
		current.dirtySeq = 0
	}
	l.files[fileID] = current
	return nil
}

// Unlink removes a file and releases all of its blocks and reservations.
func (l *Ledger) Unlink(fileID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if fileID < 0 {
		return ErrInvalidArgument
	}
	if _, ok := l.files[fileID]; !ok {
		return ErrFileNotFound
	}

	delete(l.files, fileID)
	return nil
}

// Free returns the number of blocks that are not yet allocated with indexes.
func (l *Ledger) Free() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.freeLocked()
}

// Reserved returns all delayed data blocks plus reservation-only index blocks.
func (l *Ledger) Reserved() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reservedLocked()
}

// Avail returns the amount that a new write may reserve for the caller class.
func (l *Ledger) Avail(privileged bool) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	available := l.freeLocked() - l.reservedLocked()
	if !privileged {
		available -= l.special
	}
	return maxInt64(0, available)
}

// Dirty returns dirty file IDs ordered by their unchanged write generation.
func (l *Ledger) Dirty() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	type dirtyFile struct {
		id  int64
		seq int64
	}
	files := make([]dirtyFile, 0)
	for id, state := range l.files {
		if state.delayed > 0 {
			files = append(files, dirtyFile{id: id, seq: state.dirtySeq})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].seq != files[j].seq {
			return files[i].seq < files[j].seq
		}
		return files[i].id < files[j].id
	})

	ids := make([]int64, len(files))
	for i, file := range files {
		ids[i] = file.id
	}
	return ids
}

func indexBlocks(blocks, extent int64) int64 {
	if blocks <= 0 {
		return 0
	}
	return (blocks-1)/extent + 1
}

func (l *Ledger) flushLocked(fileID int64) {
	current := l.files[fileID]
	current.allocated += current.delayed
	current.delayed = 0
	current.dirtySeq = 0
	l.files[fileID] = current
}

func (l *Ledger) oldestDirtyLocked() (int64, bool) {
	oldestID := int64(0)
	oldestSeq := int64(math.MaxInt64)
	found := false
	for id, state := range l.files {
		if state.delayed > 0 && (!found || state.dirtySeq < oldestSeq ||
			(state.dirtySeq == oldestSeq && id < oldestID)) {
			oldestID = id
			oldestSeq = state.dirtySeq
			found = true
		}
	}
	return oldestID, found
}

func (l *Ledger) delayedTotalLocked() int64 {
	var total int64
	for _, state := range l.files {
		total += state.delayed
	}
	return total
}

func (l *Ledger) usedLocked() int64 {
	var used int64
	for _, state := range l.files {
		used += state.allocated + indexBlocks(state.allocated, l.extent)
	}
	return used
}

func (l *Ledger) reservedLocked() int64 {
	var reserved int64
	for _, state := range l.files {
		reserved += state.delayed +
			indexBlocks(state.allocated+state.delayed, l.extent) -
			indexBlocks(state.allocated, l.extent)
	}
	return reserved
}

func (l *Ledger) freeLocked() int64 {
	return l.total - l.usedLocked()
}

func addNonNegative(a, b int64) (int64, bool) {
	if a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
