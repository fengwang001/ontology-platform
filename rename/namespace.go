package rename

import (
	"fmt"
	"sort"
	"sync"
)

// Namespace 是并发安全的命名空间，持有一组互不重复的名字。
// 读者通过 Snapshot 看到某个时刻的完整状态，永远观察不到含临时名的中间态。
type Namespace struct {
	mu    sync.RWMutex
	names map[string]struct{}
}

// NewNamespace 用初始名字集合创建命名空间。
func NewNamespace(names ...string) *Namespace {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return &Namespace{names: set}
}

// Snapshot 返回当前所有名字的有序副本。
func (ns *Namespace) Snapshot() []string {
	ns.mu.RLock()
	out := make([]string, 0, len(ns.names))
	for name := range ns.names {
		out = append(out, name)
	}
	ns.mu.RUnlock()
	sort.Strings(out)
	return out
}

// Has 判断名字当前是否存在。
func (ns *Namespace) Has(name string) bool {
	ns.mu.RLock()
	_, ok := ns.names[name]
	ns.mu.RUnlock()
	return ok
}

// move 执行一次原子改名：from 必须存在且 to 必须不存在。
func (ns *Namespace) move(from, to string) error {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.moveLocked(from, to)
}

// moveLocked 是 move 的持锁版本，供 Executor 在批级临界区内连续调用。
func (ns *Namespace) moveLocked(from, to string) error {
	if _, ok := ns.names[from]; !ok {
		return fmt.Errorf("rename: source %q does not exist", from)
	}
	if _, ok := ns.names[to]; ok {
		return fmt.Errorf("rename: target %q already exists", to)
	}
	delete(ns.names, from)
	ns.names[to] = struct{}{}
	return nil
}

// snapshotLocked 供 Executor 在批级临界区内生成校验用快照。
func (ns *Namespace) snapshotLocked() map[string]struct{} {
	out := make(map[string]struct{}, len(ns.names))
	for name := range ns.names {
		out[name] = struct{}{}
	}
	return out
}

// sortedSnapshotLocked 在持锁状态下返回有序名字副本，供日志记录最终状态。
func (ns *Namespace) sortedSnapshotLocked() []string {
	out := make([]string, 0, len(ns.names))
	for name := range ns.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// lockBatch / unlockBatch 是批级临界区：整个批次的所有单步改名
// 都在同一把写锁内完成，读者因此只能看到批次之前或之后的状态。
func (ns *Namespace) lockBatch()   { ns.mu.Lock() }
func (ns *Namespace) unlockBatch() { ns.mu.Unlock() }
