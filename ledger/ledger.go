package ledger

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrFileNotFound    = errors.New("file not found")
	ErrNoDelayedBlocks = errors.New("no delayed blocks")
	ErrFileTooLarge    = errors.New("file length exceeded")
	ErrOutOfSpace      = errors.New("out of space")
)

type fileState struct {
	allocated int64
	delayed   int64
	dirtySeq  int64
	dirty     *dirtyEntry
}

type dirtyEntry struct {
	fileID   int64
	dirtySeq int64
	index    int
}

type dirtyQueue []*dirtyEntry

func (q dirtyQueue) Len() int { return len(q) }

func (q dirtyQueue) Less(i, j int) bool {
	if q[i].dirtySeq == q[j].dirtySeq {
		return q[i].fileID < q[j].fileID
	}
	return q[i].dirtySeq < q[j].dirtySeq
}

func (q dirtyQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *dirtyQueue) Push(value any) {
	*q = append(*q, value.(*dirtyEntry))
}

func (q *dirtyQueue) Pop() any {
	old := *q
	last := len(old) - 1
	entry := old[last]
	*q = old[:last]
	return entry
}

type Ledger struct {
	mu             sync.Mutex
	total          int64
	systemReserved int64
	blocksPerIndex int64
	dirtyWatermark int64
	used           int64
	reserved       int64
	writeCounter   int64
	files          map[int64]*fileState
	dirtyOrder     dirtyQueue
}

func NewLedger(totalBlocks, systemReserved, blocksPerIndex, dirtyWatermark int64) (*Ledger, error) {
	if totalBlocks < 1 || systemReserved < 0 || systemReserved > totalBlocks ||
		blocksPerIndex < 1 || dirtyWatermark < 0 {
		return nil, ErrInvalidArgument
	}

	l := &Ledger{
		total:          totalBlocks,
		systemReserved: systemReserved,
		blocksPerIndex: blocksPerIndex,
		dirtyWatermark: dirtyWatermark,
		files:          make(map[int64]*fileState),
	}
	heap.Init(&l.dirtyOrder)
	return l, nil
}

func (l *Ledger) Write(fileID, blocks int64, privileged bool) ([]int64, error) {
	if fileID < 0 || blocks <= 0 {
		return nil, ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	file := l.files[fileID]
	allocated := int64(0)
	delayed := int64(0)
	if file != nil {
		allocated = file.allocated
		delayed = file.delayed
	}

	currentSize := allocated + delayed
	nextSize := currentSize + blocks
	if nextSize < currentSize || nextSize > l.total {
		return nil, ErrOutOfSpace
	}

	projectedIndex := l.indexBlocksLocked(nextSize)
	oldProjectedIndex := l.indexBlocksLocked(currentSize)
	extraReservation := blocks + projectedIndex - oldProjectedIndex

	available := l.freeLocked() - l.reserved
	if !privileged {
		available -= l.systemReserved
	}
	if extraReservation > available {
		return nil, ErrOutOfSpace
	}

	l.writeCounter++
	if file == nil {
		file = &fileState{dirty: nil}
		l.files[fileID] = file
	}

	file.allocated = allocated
	file.delayed = delayed + blocks
	if delayed == 0 {
		file.dirtySeq = l.writeCounter
		l.pushDirtyLocked(fileID)
	}

	l.reserved += blocks + projectedIndex - oldProjectedIndex

	flushed := make([]int64, 0)
	for l.totalDelayedLocked() > l.dirtyWatermark {
		oldestEntry := heap.Pop(&l.dirtyOrder).(*dirtyEntry)
		oldestID := oldestEntry.fileID
		oldest := l.files[oldestID]
		oldestEntry.index = -1
		oldest.dirty = nil
		l.flushLocked(oldestID, oldest)
		flushed = append(flushed, oldestID)
	}

	return flushed, nil
}

func (l *Ledger) Flush(fileID int64) error {
	if fileID < 0 {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	file := l.files[fileID]
	if file == nil {
		return ErrFileNotFound
	}
	if file.delayed == 0 {
		return ErrNoDelayedBlocks
	}

	l.flushLocked(fileID, file)
	return nil
}

func (l *Ledger) Truncate(fileID, blocks int64) error {
	if fileID < 0 || blocks <= 0 {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	file := l.files[fileID]
	if file == nil {
		return ErrFileNotFound
	}
	if blocks > file.allocated+file.delayed {
		return ErrFileTooLarge
	}

	oldAllocated := file.allocated
	oldDelayed := file.delayed
	newDelayed := oldDelayed - min(blocks, oldDelayed)
	remaining := blocks - (oldDelayed - newDelayed)
	newAllocated := oldAllocated - min(remaining, oldAllocated)

	oldFileReservation := oldDelayed + l.indexBlocksLocked(oldAllocated+oldDelayed) -
		l.indexBlocksLocked(oldAllocated)
	newFileReservation := newDelayed + l.indexBlocksLocked(newAllocated+newDelayed) -
		l.indexBlocksLocked(newAllocated)

	l.used += (newAllocated + l.indexBlocksLocked(newAllocated)) -
		(oldAllocated + l.indexBlocksLocked(oldAllocated))
	l.reserved += newFileReservation - oldFileReservation
	file.allocated = newAllocated
	file.delayed = newDelayed

	if newDelayed == 0 {
		l.clearDirtyLocked(fileID, file)
	}
	return nil
}

func (l *Ledger) Unlink(fileID int64) error {
	if fileID < 0 {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	file := l.files[fileID]
	if file == nil {
		return ErrFileNotFound
	}

	l.used -= file.allocated + l.indexBlocksLocked(file.allocated)
	l.reserved -= file.delayed + l.indexBlocksLocked(file.allocated+file.delayed) -
		l.indexBlocksLocked(file.allocated)
	l.clearDirtyLocked(fileID, file)
	delete(l.files, fileID)
	return nil
}

func (l *Ledger) Free() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.freeLocked()
}

func (l *Ledger) Reserved() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reserved
}

func (l *Ledger) Available(privileged bool) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	available := l.freeLocked() - l.reserved
	if !privileged {
		available -= l.systemReserved
	}
	return max(0, available)
}

func (l *Ledger) Dirty() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	fileIDs := make([]int64, 0, l.dirtyOrder.Len())
	entries := append([]*dirtyEntry(nil), l.dirtyOrder...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].dirtySeq == entries[j].dirtySeq {
			return entries[i].fileID < entries[j].fileID
		}
		return entries[i].dirtySeq < entries[j].dirtySeq
	})
	for _, entry := range entries {
		fileIDs = append(fileIDs, entry.fileID)
	}
	return fileIDs
}

func (l *Ledger) indexBlocksLocked(blocks int64) int64 {
	if blocks == 0 {
		return 0
	}
	return (blocks + l.blocksPerIndex - 1) / l.blocksPerIndex
}

func (l *Ledger) freeLocked() int64 {
	return l.total - l.used
}

func (l *Ledger) totalDelayedLocked() int64 {
	var total int64
	for _, file := range l.files {
		total += file.delayed
	}
	return total
}

func (l *Ledger) flushLocked(fileID int64, file *fileState) {
	allocated := file.allocated
	delayed := file.delayed
	oldIndex := l.indexBlocksLocked(allocated)
	newIndex := l.indexBlocksLocked(allocated + delayed)
	fileReservation := delayed + newIndex - oldIndex

	file.allocated += delayed
	file.delayed = 0
	l.used += fileReservation
	l.reserved -= fileReservation
	l.clearDirtyLocked(fileID, file)
}

func (l *Ledger) pushDirtyLocked(fileID int64) {
	file := l.files[fileID]
	if file.dirty != nil {
		return
	}
	entry := &dirtyEntry{fileID: fileID, dirtySeq: file.dirtySeq}
	heap.Push(&l.dirtyOrder, entry)
	entry.index = len(l.dirtyOrder) - 1
	file.dirty = entry
}

func (l *Ledger) clearDirtyLocked(fileID int64, file *fileState) {
	file.dirtySeq = 0
	if file.dirty != nil {
		heap.Remove(&l.dirtyOrder, file.dirty.index)
		file.dirty = nil
	}
}
