package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrNotFound         = errors.New("not found")
	ErrNoLinks          = errors.New("inode has no links")
	ErrShrinkInProgress = errors.New("shrink already in progress")
	ErrNoPendingShrink  = errors.New("no pending shrink")
	ErrLinksFull        = errors.New("link limit reached")
	ErrOrphanListFull   = errors.New("orphan list is full")
	ErrNoSpace          = errors.New("not enough block pool space")
)

type CrashAction string

const (
	CrashDelete   CrashAction = "delete"
	CrashTruncate CrashAction = "truncate"
)

type CrashResult struct {
	Inode  int
	Action CrashAction
}

type inode struct {
	links   int
	opens   int
	blocks  int
	pending *int
}

type Ledger struct {
	mu sync.Mutex

	totalBlocks int
	orphanLimit int
	linkLimit   int

	usedBlocks int
	nextInode  int
	nextHandle int
	inodes     map[int]*inode
	handles    map[int]int
	orphans    []int
	members    map[int]struct{}
}

func NewLedger(totalBlocks, orphanCapacity, linkLimit int) (*Ledger, error) {
	if totalBlocks < 1 || orphanCapacity < 1 || linkLimit < 1 {
		return nil, ErrInvalidArgument
	}

	return &Ledger{
		totalBlocks: totalBlocks,
		orphanLimit: orphanCapacity,
		linkLimit:   linkLimit,
		nextInode:   1,
		nextHandle:  1,
		inodes:      make(map[int]*inode),
		handles:     make(map[int]int),
		members:     make(map[int]struct{}),
	}, nil
}

func (l *Ledger) Create(blocks int) (int, error) {
	if blocks < 0 {
		return 0, ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.usedBlocks+blocks > l.totalBlocks {
		return 0, ErrNoSpace
	}

	id := l.nextInode
	l.nextInode++
	l.inodes[id] = &inode{
		links:  1,
		blocks: blocks,
	}
	l.usedBlocks += blocks
	return id, nil
}

func (l *Ledger) Link(id int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return ErrNotFound
	}
	if node.links == 0 {
		return ErrNoLinks
	}
	if node.links >= l.linkLimit {
		return ErrLinksFull
	}

	node.links++
	return nil
}

func (l *Ledger) Unlink(id int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return ErrNotFound
	}
	if node.links == 0 {
		return ErrNoLinks
	}

	if node.links == 1 && node.opens > 0 {
		if _, isMember := l.members[id]; !isMember && len(l.orphans) >= l.orphanLimit {
			return ErrOrphanListFull
		}
	}

	node.links--
	if node.links == 0 && node.opens == 0 {
		l.deleteInode(id)
		return nil
	}

	l.syncOrphan(id)
	return nil
}

func (l *Ledger) Open(id int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return 0, ErrNotFound
	}
	if node.links == 0 {
		return 0, ErrNoLinks
	}

	handle := l.nextHandle
	l.nextHandle++
	l.handles[handle] = id
	node.opens++
	return handle, nil
}

func (l *Ledger) Close(handle int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	id, ok := l.handles[handle]
	if !ok {
		return ErrNotFound
	}
	node := l.inodes[id]
	if node == nil {
		return ErrNotFound
	}

	delete(l.handles, handle)
	node.opens--
	if node.links == 0 && node.opens == 0 {
		l.deleteInode(id)
		return nil
	}

	l.syncOrphan(id)
	return nil
}

func (l *Ledger) Shrink(id int, blocks int) error {
	if blocks < 0 {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return ErrNotFound
	}
	if blocks > node.blocks {
		return ErrInvalidArgument
	}
	if node.pending != nil {
		return ErrShrinkInProgress
	}

	l.usedBlocks -= node.blocks - blocks
	node.blocks = blocks
	return nil
}

func (l *Ledger) BeginShrink(id int, blocks int) error {
	if blocks < 0 {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return ErrNotFound
	}
	if blocks > node.blocks {
		return ErrInvalidArgument
	}
	if node.pending != nil {
		return ErrShrinkInProgress
	}

	_, isMember := l.members[id]
	if !isMember && len(l.orphans) >= l.orphanLimit {
		return ErrOrphanListFull
	}

	pending := blocks
	node.pending = &pending
	l.syncOrphan(id)
	return nil
}

func (l *Ledger) FinishShrink(id int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return ErrNotFound
	}
	if node.pending == nil {
		return ErrNoPendingShrink
	}

	target := *node.pending
	node.pending = nil
	l.usedBlocks -= node.blocks - target
	node.blocks = target
	l.syncOrphan(id)
	return nil
}

func (l *Ledger) Crash() []CrashResult {
	l.mu.Lock()
	defer l.mu.Unlock()

	results := make([]CrashResult, 0, len(l.orphans))
	orphanIDs := append([]int(nil), l.orphans...)
	for _, id := range orphanIDs {
		node := l.inodes[id]
		if node == nil {
			continue
		}

		if node.links == 0 {
			results = append(results, CrashResult{Inode: id, Action: CrashDelete})
			l.deleteInode(id)
			continue
		}

		target := 0
		if node.pending != nil {
			target = *node.pending
		}
		l.usedBlocks -= node.blocks - target
		node.blocks = target
		node.pending = nil
		node.opens = 0
		results = append(results, CrashResult{Inode: id, Action: CrashTruncate})
	}

	for _, node := range l.inodes {
		node.opens = 0
	}
	l.handles = make(map[int]int)
	l.nextHandle = 1
	l.orphans = nil
	l.members = make(map[int]struct{})
	return results
}

func (l *Ledger) Used() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.usedBlocks
}

func (l *Ledger) Exists(id int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inodes[id] != nil
}

func (l *Ledger) Links(id int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return 0, ErrNotFound
	}
	return node.links, nil
}

func (l *Ledger) OpenCount(id int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return 0, ErrNotFound
	}
	return node.opens, nil
}

func (l *Ledger) Blocks(id int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return 0, ErrNotFound
	}
	return node.blocks, nil
}

func (l *Ledger) PendingShrink(id int) (int, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node := l.inodes[id]
	if node == nil {
		return 0, false, ErrNotFound
	}
	if node.pending == nil {
		return 0, false, nil
	}
	return *node.pending, true, nil
}

func (l *Ledger) OrphanIDs() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.orphans...)
}

func (l *Ledger) HandleInode(handle int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	id, ok := l.handles[handle]
	if !ok {
		return 0, ErrNotFound
	}
	if l.inodes[id] == nil {
		return 0, ErrNotFound
	}
	return id, nil
}

func (l *Ledger) syncOrphan(id int) {
	node := l.inodes[id]
	member := node != nil && (node.pending != nil || node.links == 0 && node.opens > 0)

	if member {
		if _, ok := l.members[id]; !ok {
			l.orphans = append([]int{id}, l.orphans...)
			l.members[id] = struct{}{}
		}
		return
	}

	l.removeOrphan(id)
}

func (l *Ledger) removeOrphan(id int) {
	delete(l.members, id)
	for index, current := range l.orphans {
		if current == id {
			l.orphans = append(l.orphans[:index], l.orphans[index+1:]...)
			return
		}
	}
}

func (l *Ledger) deleteInode(id int) {
	node := l.inodes[id]
	if node == nil {
		return
	}

	l.usedBlocks -= node.blocks
	delete(l.inodes, id)
	l.removeOrphan(id)
}
