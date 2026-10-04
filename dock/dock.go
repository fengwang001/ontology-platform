package dock

import (
	"container/heap"
	"errors"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrState           = errors.New("state conflict")
	ErrConflict        = errors.New("conflict")
)

type Kind uint8

const (
	Dry Kind = iota
	Reefer
)

type Assignment struct {
	Dock []byte
	Kind Kind
}

type Pool struct {
	mu      sync.Mutex
	maxNow  int64
	docks   map[string]*dockInfo
	freeDry stringHeap
	freeRef stringHeap
	used    map[string]*dockInfo
}

type dockInfo struct {
	id    []byte
	kind  Kind
	free  bool
	truck []byte
}

type stringHeap [][]byte

func (h stringHeap) Len() int {
	return len(h)
}

func (h stringHeap) Less(i, j int) bool {
	return string(h[i]) < string(h[j])
}

func (h stringHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *stringHeap) Push(value any) {
	*h = append(*h, value.([]byte))
}

func (h *stringHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func NewPool() *Pool {
	return &Pool{
		docks: make(map[string]*dockInfo),
		used:  make(map[string]*dockInfo),
	}
}

func (p *Pool) AddDock(dockID []byte, kind Kind, now int64) error {
	if !validDock(dockID) || !validKind(kind) || !bounded(now, 0, 1000000000) {
		return ErrInvalidArgument
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if now < p.maxNow {
		return ErrClockRollback
	}
	key := string(dockID)
	if p.docks[key] != nil {
		return ErrConflict
	}

	info := &dockInfo{
		id:   append([]byte(nil), dockID...),
		kind: kind,
		free: true,
	}
	p.docks[key] = info
	if kind == Dry {
		heap.Push(&p.freeDry, info.id)
	} else {
		heap.Push(&p.freeRef, info.id)
	}
	p.maxNow = now
	return nil
}

func (p *Pool) Assign(truck []byte, kind Kind, allowReeferBorrow bool) (Assignment, bool) {
	if !validKind(kind) {
		return Assignment{}, false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if kind == Dry && p.freeDry.Len() > 0 {
		return p.pop(truck, &p.freeDry, Dry), true
	}
	if p.freeRef.Len() > 0 && (kind == Reefer || allowReeferBorrow) {
		return p.pop(truck, &p.freeRef, Reefer), true
	}
	return Assignment{}, false
}

func (p *Pool) Release(truck []byte, now int64) ([]byte, Kind, error) {
	if !validTruck(truck) || !bounded(now, 0, 1000000000) {
		return nil, Dry, ErrInvalidArgument
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if now < p.maxNow {
		return nil, Dry, ErrClockRollback
	}
	info := p.used[string(truck)]
	if info == nil {
		return nil, Dry, ErrState
	}

	dockID := info.id
	dockKind := info.kind
	delete(p.used, string(truck))
	info.truck = nil
	info.free = true
	if dockKind == Dry {
		heap.Push(&p.freeDry, info.id)
	} else {
		heap.Push(&p.freeRef, info.id)
	}
	p.maxNow = now
	return append([]byte(nil), dockID...), dockKind, nil
}

func (p *Pool) HasFree(kind Kind) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if kind == Dry {
		return p.freeDry.Len() > 0
	}
	return p.freeRef.Len() > 0
}

func (p *Pool) assignTo(info *dockInfo, dockID []byte, truck []byte) {
	info.free = false
	info.truck = append([]byte(nil), truck...)
	p.used[string(truck)] = info
}

func (p *Pool) pop(truck []byte, free *stringHeap, kind Kind) Assignment {
	dockID := heap.Pop(free).([]byte)
	info := p.docks[string(dockID)]
	p.assignTo(info, dockID, truck)
	return Assignment{Dock: dockID, Kind: kind}
}

func validDock(dockID []byte) bool {
	return len(dockID) >= 1 && len(dockID) <= 32
}

func validTruck(truck []byte) bool {
	return len(truck) >= 1 && len(truck) <= 32
}

func validKind(kind Kind) bool {
	return kind == Dry || kind == Reefer
}

func bounded(value, low, high int64) bool {
	return value >= low && value <= high
}
