// Package snapshotfs 实现带持有计数与回滚的写时复制（copy-on-write）
// 文件系统快照空间账本。
//
// 每个块记录出生事务号 b 与死亡事务号 d（d == infinity 表示存活）。
// 块在事务号 t 存活当且仅当 b <= t < d；快照 s（事务号 t_s）包含块
// 当且仅当该块在 t_s 存活（且未被回滚丢弃）。
package snapshotfs

import "sync"

// MaxCap 与 MaxAlloc 限定构造容量与单次分配大小（均含端点）。
const (
	MaxCap   = int64(1) << 50
	MaxAlloc = int64(1) << 40
)

// infinity 表示死亡号 d 暂为无穷。
const infinity = int64(1 << 62)

// block 记录一个已编号块的元数据。
type block struct {
	id        int64
	size      int64
	birth     int64 // b
	death     int64 // d，等于 infinity 表示存活
	discarded bool  // 被 Rollback 丢弃
}

// snapshot 记录一个未销毁快照。
type snapshot struct {
	name string
	t    int64
	hold int
}

// Ledger 是并发安全的快照空间账本。
type Ledger struct {
	mu        sync.RWMutex
	cap       int64
	cur       int64
	maxID     int64 // 已消耗（成功 Alloc）的最大编号
	blocks    map[int64]*block
	snapshots map[string]*snapshot
}

// New 创建容量为 cap 的账本；cap 超出 [1, 2^50] 时整体拒绝。
func New(cap int64) (*Ledger, error) {
	if cap < 1 || cap > MaxCap {
		return nil, ErrInvalidArgument
	}
	return &Ledger{
		cap:       cap,
		cur:       1,
		blocks:    make(map[int64]*block),
		snapshots: make(map[string]*snapshot),
	}, nil
}

// Alloc 分配一个 size 字节的块并返回其编号（从 1 起连续）。
func (l *Ledger) Alloc(size int64) (int64, error) {
	if size < 1 || size > MaxAlloc {
		return 0, ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.usedLocked()+size > l.cap {
		return 0, ErrOutOfSpace
	}
	l.maxID++
	l.blocks[l.maxID] = &block{
		id:    l.maxID,
		size:  size,
		birth: l.cur,
		death: infinity,
	}
	return l.maxID, nil
}

// Free 将存活块的死亡号置为当前事务号。
func (l *Ledger) Free(id int64) error {
	if id < 1 {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if id > l.maxID {
		return ErrNotFound
	}
	blk := l.blocks[id]
	if blk.discarded {
		return ErrDiscarded
	}
	if blk.death != infinity {
		return ErrDead
	}
	blk.death = l.cur
	return nil
}

// Snapshot 在当前事务号建立快照并将 cur 加 1。
func (l *Ledger) Snapshot(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.snapshots[name]; ok {
		return ErrSnapshotExists
	}
	l.snapshots[name] = &snapshot{name: name, t: l.cur}
	l.cur++
	return nil
}

// Destroy 销毁快照，不再被持有的块随即释放。
func (l *Ledger) Destroy(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.snapshots[name]
	if !ok {
		return ErrSnapshotMissing
	}
	if s.hold > 0 {
		return ErrHeld
	}
	delete(l.snapshots, name)
	return nil
}

// Referenced 返回快照所含全部（未丢弃）块的大小之和。
func (l *Ledger) Referenced(name string) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.snapshots[name]
	if !ok {
		return 0, ErrSnapshotMissing
	}
	var total int64
	for _, blk := range l.blocks {
		if l.containsLocked(s, blk) {
			total += blk.size
		}
	}
	return total, nil
}

// Unique 返回此刻销毁该快照将被释放的字节数。
func (l *Ledger) Unique(name string) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.snapshots[name]
	if !ok {
		return 0, ErrSnapshotMissing
	}
	return l.uniqueLocked(s), nil
}

// Hold 使快照持有计数加 1。
func (l *Ledger) Hold(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.snapshots[name]
	if !ok {
		return ErrSnapshotMissing
	}
	s.hold++
	return nil
}

// Release 使快照持有计数减 1；计数为 0 时报 ErrNotHeld。
func (l *Ledger) Release(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.snapshots[name]
	if !ok {
		return ErrSnapshotMissing
	}
	if s.hold == 0 {
		return ErrNotHeld
	}
	s.hold--
	return nil
}

// Rollback 把现状回滚到指定快照。
func (l *Ledger) Rollback(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	target, ok := l.snapshots[name]
	if !ok {
		return ErrSnapshotMissing
	}
	// name 自身被持有不阻止回滚；其余事务号更大的未销毁快照被持有时拒绝。
	for _, s := range l.snapshots {
		if s.t > target.t && s.hold > 0 {
			return ErrHeld
		}
	}
	t := target.t
	// 1. 销毁事务号大于 t_name 的全部未销毁快照。
	for nm, s := range l.snapshots {
		if s.t > t {
			delete(l.snapshots, nm)
		}
	}
	// 2/3. 丢弃 b > t 的块；快照包含的块（b <= t 且 d > t）死亡号恢复为无穷。
	for _, blk := range l.blocks {
		if blk.discarded {
			continue
		}
		if blk.birth > t {
			blk.discarded = true
		} else if blk.death > t {
			blk.death = infinity
		}
	}
	return nil
}

// Used 返回当前被持有（占用）的字节数。
func (l *Ledger) Used() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.usedLocked()
}

// Cur 返回当前事务号。
func (l *Ledger) Cur() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cur
}

// aliveLocked 报告块在事务号 t 是否存活：b <= t < d。
func aliveLocked(blk *block, t int64) bool {
	return !blk.discarded && blk.birth <= t && t < blk.death
}

// containsLocked 报告快照 s 是否包含块。
func (l *Ledger) containsLocked(s *snapshot, blk *block) bool {
	return aliveLocked(blk, s.t)
}

// usedLocked 按定义重算已占用字节：
// 块被持有当且仅当 d 为无穷（现存）或存在包含它的未销毁快照。
func (l *Ledger) usedLocked() int64 {
	var total int64
	for _, blk := range l.blocks {
		if blk.discarded {
			continue
		}
		if blk.death == infinity {
			total += blk.size
			continue
		}
		for _, s := range l.snapshots {
			if l.containsLocked(s, blk) {
				total += blk.size
				break
			}
		}
	}
	return total
}

// uniqueLocked 计算销毁快照 s 将释放的字节：d 有限且包含它的
// 未销毁快照只有 s 一个的块。
func (l *Ledger) uniqueLocked(target *snapshot) int64 {
	var total int64
	for _, blk := range l.blocks {
		if blk.discarded || blk.death == infinity {
			continue
		}
		if !l.containsLocked(target, blk) {
			continue
		}
		sole := true
		for _, s := range l.snapshots {
			if s == target {
				continue
			}
			if l.containsLocked(s, blk) {
				sole = false
				break
			}
		}
		if sole {
			total += blk.size
		}
	}
	return total
}
