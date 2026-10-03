// Package inodeledger 实现带孤儿链表与两阶段截断的 inode 生命周期账本。
package inodeledger

import (
	"errors"
	"sync"
)

// 拒绝原因，均可用 errors.Is 区分。
var (
	ErrInvalidArgument   = errors.New("inodeledger: invalid argument")
	ErrNotFound          = errors.New("inodeledger: not found")
	ErrNoLinks           = errors.New("inodeledger: inode has no links")
	ErrShrinkInProgress  = errors.New("inodeledger: shrink already in progress")
	ErrNoPendingShrink   = errors.New("inodeledger: no pending shrink")
	ErrLinkLimit         = errors.New("inodeledger: link count limit reached")
	ErrOrphanListFull    = errors.New("inodeledger: orphan list full")
	ErrInsufficientSpace = errors.New("inodeledger: insufficient block pool space")
)

// CrashAction 表示 Crash 回收时对孤儿链表成员采取的动作。
type CrashAction int

const (
	// ActionDelete 表示链接数为 0，释放全部块并删除 inode。
	ActionDelete CrashAction = iota
	// ActionTruncate 表示链接数不小于 1，按登记完成截断并保留 inode。
	ActionTruncate
)

func (a CrashAction) String() string {
	if a == ActionDelete {
		return "删除"
	}
	return "截断"
}

// CrashRecord 是 Crash 返回的一条回收记录。
type CrashRecord struct {
	Inode  int
	Action CrashAction
}

// Info 是单个 inode 的快照，供查询与测试使用。
type Info struct {
	Links         int
	Opens         int
	Blocks        int
	Pending       bool
	PendingBlocks int
	InOrphanList  bool
}

type inode struct {
	links         int
	opens         int
	blocks        int
	pending       bool
	pendingBlocks int
	inList        bool
}

// Ledger 是 inode 生命周期账本，所有方法可并发调用。
type Ledger struct {
	mu         sync.Mutex
	pool       int
	capacity   int
	linkLimit  int
	used       int
	inodes     map[int]*inode
	handles    map[int]int
	orphans    []int // 头为最近进入者
	nextInode  int
	nextHandle int
}

// New 构造账本。P、K、L 均须不小于 1，否则整体拒绝。
func New(P, K, L int) (*Ledger, error) {
	if P < 1 || K < 1 || L < 1 {
		return nil, ErrInvalidArgument
	}
	return &Ledger{
		pool:       P,
		capacity:   K,
		linkLimit:  L,
		inodes:     make(map[int]*inode),
		handles:    make(map[int]int),
		nextInode:  1,
		nextHandle: 1,
	}, nil
}

// Create 创建一个占用 b 个块的 inode，返回从 1 起连续的编号。
func (l *Ledger) Create(b int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b < 0 {
		return 0, ErrInvalidArgument
	}
	if l.used+b > l.pool {
		return 0, ErrInsufficientSpace
	}
	id := l.nextInode
	l.nextInode++
	l.inodes[id] = &inode{links: 1, blocks: b}
	l.used += b
	return id, nil
}

// Link 使链接数加 1。
func (l *Ledger) Link(i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	in, ok := l.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if in.links == 0 {
		return ErrNoLinks
	}
	if in.links+1 > l.linkLimit {
		return ErrLinkLimit
	}
	in.links++
	return nil
}

// Unlink 使链接数减 1。
func (l *Ledger) Unlink(i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	in, ok := l.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if in.links == 0 {
		return ErrNoLinks
	}
	if in.links == 1 && in.opens > 0 && !in.inList && len(l.orphans) >= l.capacity {
		return ErrOrphanListFull
	}
	in.links--
	if in.links > 0 {
		return nil
	}
	if in.opens == 0 {
		l.deleteInode(i, in)
		return nil
	}
	l.insertOrphan(i, in)
	return nil
}

// Open 返回从 1 起连续的新句柄编号并使打开数加 1。
func (l *Ledger) Open(i int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	in, ok := l.inodes[i]
	if !ok {
		return 0, ErrNotFound
	}
	if in.links == 0 {
		return 0, ErrNoLinks
	}
	h := l.nextHandle
	l.nextHandle++
	l.handles[h] = i
	in.opens++
	return h, nil
}

// Close 使句柄所属 inode 的打开数减 1 并使句柄失效。
func (l *Ledger) Close(h int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	i, ok := l.handles[h]
	if !ok {
		return ErrNotFound
	}
	delete(l.handles, h)
	in := l.inodes[i]
	in.opens--
	if in.opens == 0 && in.links == 0 {
		l.deleteInode(i, in)
	}
	return nil
}

// Shrink 把块数立即缩到 nb 并归还块池。
func (l *Ledger) Shrink(i, nb int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if nb < 0 {
		return ErrInvalidArgument
	}
	in, ok := l.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if nb > in.blocks {
		return ErrInvalidArgument
	}
	if in.pending {
		return ErrShrinkInProgress
	}
	l.used -= in.blocks - nb
	in.blocks = nb
	return nil
}

// BeginShrink 登记待完成截断到 nb，块数暂不变。
func (l *Ledger) BeginShrink(i, nb int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if nb < 0 {
		return ErrInvalidArgument
	}
	in, ok := l.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if nb > in.blocks {
		return ErrInvalidArgument
	}
	if in.pending {
		return ErrShrinkInProgress
	}
	if !in.inList && len(l.orphans) >= l.capacity {
		return ErrOrphanListFull
	}
	in.pending = true
	in.pendingBlocks = nb
	l.insertOrphan(i, in)
	return nil
}

// FinishShrink 完成登记的截断并清除登记。
func (l *Ledger) FinishShrink(i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	in, ok := l.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if !in.pending {
		return ErrNoPendingShrink
	}
	l.used -= in.blocks - in.pendingBlocks
	in.blocks = in.pendingBlocks
	in.pending = false
	in.pendingBlocks = 0
	if !(in.links == 0 && in.opens > 0) {
		l.removeOrphan(i, in)
	}
	return nil
}

// Crash 模拟断电重启，按孤儿链表头到尾回收并返回记录。
func (l *Ledger) Crash() []CrashRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handles = make(map[int]int)
	for _, in := range l.inodes {
		in.opens = 0
	}
	var records []CrashRecord
	for _, id := range l.orphans {
		in := l.inodes[id]
		in.inList = false
		if in.links == 0 {
			l.used -= in.blocks
			delete(l.inodes, id)
			records = append(records, CrashRecord{Inode: id, Action: ActionDelete})
		} else {
			l.used -= in.blocks - in.pendingBlocks
			in.blocks = in.pendingBlocks
			in.pending = false
			in.pendingBlocks = 0
			records = append(records, CrashRecord{Inode: id, Action: ActionTruncate})
		}
	}
	l.orphans = nil
	return records
}

// Used 返回已占用块数。
func (l *Ledger) Used() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

// Orphans 返回孤儿链表成员（自头到尾）。
func (l *Ledger) Orphans() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.orphans...)
}

// InfoOf 返回 inode 快照；ok 为 false 表示不存在。
func (l *Ledger) InfoOf(i int) (Info, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	in, ok := l.inodes[i]
	if !ok {
		return Info{}, false
	}
	return Info{
		Links:         in.links,
		Opens:         in.opens,
		Blocks:        in.blocks,
		Pending:       in.pending,
		PendingBlocks: in.pendingBlocks,
		InOrphanList:  in.inList,
	}, true
}

// insertOrphan 在 inode 不在链表中时将其头插，否则位置不变。
func (l *Ledger) insertOrphan(i int, in *inode) {
	if in.inList {
		return
	}
	in.inList = true
	l.orphans = append([]int{i}, l.orphans...)
}

// removeOrphan 将 inode 从链表摘除，其余成员相对次序不变。
func (l *Ledger) removeOrphan(i int, in *inode) {
	if !in.inList {
		return
	}
	in.inList = false
	for idx, id := range l.orphans {
		if id == i {
			l.orphans = append(l.orphans[:idx], l.orphans[idx+1:]...)
			return
		}
	}
}

// deleteInode 释放全部块并删除 inode，连同截断登记与链表位置。
func (l *Ledger) deleteInode(i int, in *inode) {
	l.used -= in.blocks
	in.pending = false
	in.pendingBlocks = 0
	l.removeOrphan(i, in)
	delete(l.inodes, i)
}
