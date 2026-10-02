package ontology

import (
	"container/heap"
	"errors"
	"slices"
	"sync"
)

type SplitState uint8

const (
	Unassigned SplitState = iota
	Assigned
	Finished
	Quarantined
)

type RequestResultKind uint8

const (
	RequestAssigned RequestResultKind = iota
	RequestWaiting
	RequestNoMoreSplits
)

var (
	ErrInvalidArgument          = errors.New("invalid argument")
	ErrSealed                   = errors.New("sealed")
	ErrDuplicateSplit           = errors.New("duplicate split")
	ErrReaderFailed             = errors.New("reader is failed")
	ErrReaderAlive              = errors.New("reader is alive")
	ErrSplitNotAssignedToReader = errors.New("split is not assigned to reader")
	ErrCheckpointOutOfOrder     = errors.New("checkpoint is out of order")
	ErrCheckpointAhead          = errors.New("checkpoint is ahead of latest triggered checkpoint")
	ErrCheckpointStale          = errors.New("checkpoint is not after completed checkpoint")
)

type SplitInfo struct {
	State SplitState
	Owner int
	Ret   int
}

type RequestResult struct {
	Kind  RequestResultKind
	Split int64
}

type ReaderFailedResult struct {
	Returned    []int64
	Quarantined []int64
	Revoked     []int64
}

type splitRecord struct {
	state SplitState
	owner int
	at    uint64
	ft    uint64
	ret   int
}

type splitHeap []int64

func (h splitHeap) Len() int           { return len(h) }
func (h splitHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h splitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *splitHeap) Push(value any) {
	*h = append(*h, value.(int64))
}

func (h *splitHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

type Allocator struct {
	mu sync.RWMutex

	readers          int
	quarantineAfter  int
	records          map[int64]*splitRecord
	unassigned       []splitHeap
	owned            []splitHeap
	quarantinedHeap  splitHeap
	counts           [4]int
	alive            []bool
	latestCheckpoint int64
	doneCheckpoint   int64
	sealed           bool

	probes     int64
	ownedCount int64
}

func NewAllocator(readers int, quarantineAfter int) (*Allocator, error) {
	if readers < 1 || readers > 64 || quarantineAfter < 1 || quarantineAfter > 100 {
		return nil, ErrInvalidArgument
	}

	allocator := &Allocator{
		readers:         readers,
		quarantineAfter: quarantineAfter,
		records:         make(map[int64]*splitRecord),
		unassigned:      make([]splitHeap, readers),
		owned:           make([]splitHeap, readers),
		alive:           make([]bool, readers),
	}
	for reader := range allocator.alive {
		allocator.alive[reader] = true
	}
	return allocator, nil
}

func validSplit(split int64) bool {
	return split >= 0 && split <= 1_000_000_000
}

func (a *Allocator) AddSplits(splits []int64) error {
	if len(splits) < 1 || len(splits) > 1000 {
		return ErrInvalidArgument
	}
	for _, split := range splits {
		if !validSplit(split) {
			return ErrInvalidArgument
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.sealed {
		return ErrSealed
	}
	seen := make(map[int64]struct{}, len(splits))
	for _, split := range splits {
		if _, exists := a.records[split]; exists {
			return ErrDuplicateSplit
		}
		if _, exists := seen[split]; exists {
			return ErrDuplicateSplit
		}
		seen[split] = struct{}{}
	}

	for _, split := range splits {
		record := &splitRecord{state: Unassigned, owner: -1}
		a.records[split] = record
		reader := int(split % int64(a.readers))
		heap.Push(&a.unassigned[reader], split)
	}
	a.counts[Unassigned] += len(splits)
	return nil
}

func (a *Allocator) Seal() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealed = true
}

func (a *Allocator) Checkpoint(cp int64) error {
	if cp < 1 {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if cp != a.latestCheckpoint+1 {
		return ErrCheckpointOutOfOrder
	}
	a.latestCheckpoint = cp
	return nil
}

func (a *Allocator) Complete(cp int64) error {
	if cp < 1 {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if cp > a.latestCheckpoint {
		return ErrCheckpointAhead
	}
	if cp <= a.doneCheckpoint {
		return ErrCheckpointStale
	}
	a.doneCheckpoint = cp
	return nil
}

func (a *Allocator) ReaderRestarted(reader int) error {
	if reader < 0 || reader >= a.readers {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.alive[reader] {
		return ErrReaderAlive
	}
	a.alive[reader] = true
	return nil
}

func (a *Allocator) cleanUnassigned(reader int) {
	for a.unassigned[reader].Len() > 0 {
		split := a.unassigned[reader][0]
		record := a.records[split]
		if record.state == Unassigned && split%int64(a.readers) == int64(reader) {
			return
		}
		heap.Pop(&a.unassigned[reader])
	}
}

func (a *Allocator) cleanOwned(reader int) {
	for a.owned[reader].Len() > 0 {
		split := a.owned[reader][0]
		record := a.records[split]
		if record.owner == reader && (record.state == Assigned || record.state == Finished) {
			return
		}
		heap.Pop(&a.owned[reader])
	}
}

func (a *Allocator) RequestSplit(reader int) (RequestResult, error) {
	if reader < 0 || reader >= a.readers {
		return RequestResult{}, ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.alive[reader] {
		return RequestResult{}, ErrReaderFailed
	}

	a.cleanUnassigned(reader)
	bestSplit := int64(-1)
	bestReader := -1
	if a.unassigned[reader].Len() > 0 {
		bestSplit = a.unassigned[reader][0]
		bestReader = reader
		a.probes++
	} else {
		for preferred := 0; preferred < a.readers; preferred++ {
			if preferred == reader || a.alive[preferred] {
				continue
			}
			a.cleanUnassigned(preferred)
			if a.unassigned[preferred].Len() == 0 {
				continue
			}
			a.probes++
			candidate := a.unassigned[preferred][0]
			if bestReader == -1 || candidate < bestSplit {
				bestSplit = candidate
				bestReader = preferred
			}
		}
	}

	if bestReader == -1 {
		if a.sealed && a.counts[Unassigned] == 0 && a.counts[Assigned] == 0 {
			return RequestResult{Kind: RequestNoMoreSplits}, nil
		}
		return RequestResult{Kind: RequestWaiting}, nil
	}

	heap.Pop(&a.unassigned[bestReader])
	record := a.records[bestSplit]
	record.state = Assigned
	record.owner = reader
	record.at = uint64(a.latestCheckpoint) + 1
	a.counts[Unassigned]--
	a.counts[Assigned]++
	heap.Push(&a.owned[reader], bestSplit)

	return RequestResult{Kind: RequestAssigned, Split: bestSplit}, nil
}

func (a *Allocator) Finished(reader int, split int64) error {
	if reader < 0 || reader >= a.readers || !validSplit(split) {
		return ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.alive[reader] {
		return ErrReaderFailed
	}
	record := a.records[split]
	if record == nil || record.state != Assigned || record.owner != reader {
		return ErrSplitNotAssignedToReader
	}

	record.state = Finished
	record.ft = uint64(a.latestCheckpoint) + 1
	a.counts[Assigned]--
	a.counts[Finished]++
	return nil
}

func (a *Allocator) ReaderFailed(reader int) (ReaderFailedResult, error) {
	if reader < 0 || reader >= a.readers {
		return ReaderFailedResult{}, ErrInvalidArgument
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.alive[reader] {
		return ReaderFailedResult{}, ErrReaderFailed
	}
	a.alive[reader] = false

	result := ReaderFailedResult{
		Returned:    []int64{},
		Quarantined: []int64{},
		Revoked:     []int64{},
	}
	retained := make([]int64, 0)

	for {
		a.cleanOwned(reader)
		if a.owned[reader].Len() == 0 {
			break
		}
		split := heap.Pop(&a.owned[reader]).(int64)
		record := a.records[split]
		a.ownedCount++

		if record.at > uint64(a.doneCheckpoint) {
			result.Returned = append(result.Returned, split)
			record.owner = -1
			record.ret++
			if record.state == Finished {
				a.counts[Finished]--
			} else {
				a.counts[Assigned]--
			}
			if record.ret >= a.quarantineAfter {
				record.state = Quarantined
				a.counts[Quarantined]++
				heap.Push(&a.quarantinedHeap, split)
				result.Quarantined = append(result.Quarantined, split)
			} else {
				record.state = Unassigned
				a.counts[Unassigned]++
				preferred := int(split % int64(a.readers))
				heap.Push(&a.unassigned[preferred], split)
			}
			continue
		}

		if record.state == Finished && record.ft > uint64(a.doneCheckpoint) {
			record.state = Assigned
			a.counts[Finished]--
			a.counts[Assigned]++
			result.Revoked = append(result.Revoked, split)
		}
		retained = append(retained, split)
	}

	for _, split := range retained {
		heap.Push(&a.owned[reader], split)
	}
	return result, nil
}

func (a *Allocator) State(split int64) (SplitInfo, error) {
	if !validSplit(split) {
		return SplitInfo{}, ErrInvalidArgument
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	record := a.records[split]
	if record == nil {
		return SplitInfo{}, ErrInvalidArgument
	}
	return SplitInfo{State: record.state, Owner: record.owner, Ret: record.ret}, nil
}

func (a *Allocator) cleanQuarantinedLocked() {
	for a.quarantinedHeap.Len() > 0 {
		split := a.quarantinedHeap[0]
		if a.records[split].state == Quarantined {
			return
		}
		heap.Pop(&a.quarantinedHeap)
	}
}

func (a *Allocator) Quarantined() []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.cleanQuarantinedLocked()
	result := append([]int64(nil), a.quarantinedHeap...)
	slices.Sort(result)
	return result
}

func (a *Allocator) Counts() map[SplitState]int {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return map[SplitState]int{
		Unassigned:  a.counts[Unassigned],
		Assigned:    a.counts[Assigned],
		Finished:    a.counts[Finished],
		Quarantined: a.counts[Quarantined],
	}
}
