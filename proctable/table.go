// Package proctable 实现一个带僵尸回收的进程表。
//
// 进程表容量为 N（含存活与僵尸表项）。始进程编号为 1，建表时已存在且
// 永不退出。进程退出后成为僵尸并保留表项与退出码；其全部子进程（存活
// 与僵尸）改挂到始进程，改挂后归始进程的僵尸立即被回收并释放表项；若
// 退出者自己的父进程就是始进程，它也立即被回收。
//
// 所有公开方法均可并发调用；相同的操作序列重放得到完全相同的表与回收
// 顺序。
package proctable

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// InitPID 是始进程的编号，建表时即存在，永不退出。
const InitPID = 1

// 可区分的拒绝原因。多种原因同时成立时，按
// 「不存在、已是僵尸、其余」的固定顺序只报第一个。
var (
	// ErrNotExist 表示指定的进程不存在（从未创建或已被回收）。
	ErrNotExist = errors.New("process does not exist")
	// ErrZombie 表示目标是僵尸进程，不能再对其创建、退出或等待。
	ErrZombie = errors.New("process is a zombie")
	// ErrTableFull 表示进程表已满，无法创建新进程。
	ErrTableFull = errors.New("process table is full")
	// ErrInitExit 表示试图让始进程退出。
	ErrInitExit = errors.New("init process cannot exit")
	// ErrNotChild 表示等待的目标不是调用者的子进程。
	ErrNotChild = errors.New("target is not a child of the caller")
	// ErrChildAlive 表示指定等待的子进程仍然存活。
	ErrChildAlive = errors.New("child is still alive")
	// ErrNoChildren 表示等待任一子进程时调用者没有任何子进程。
	ErrNoChildren = errors.New("no children")
	// ErrNoZombie 表示等待任一子进程时仍有存活子进程但暂无僵尸。
	ErrNoZombie = errors.New("no zombie child yet, but alive children exist")
)

// Entry 是进程表中一个表项的快照。
type Entry struct {
	PID      int  // 进程编号，自 2 起严格递增、不复用（始进程为 1）
	Parent   int  // 父进程编号；除始进程外父进程必然存活
	Alive    bool // 是否存活；false 表示僵尸
	ExitCode int  // 退出码，仅僵尸有效
}

// Table 是容量受限的进程表，所有方法并发安全。
type Table struct {
	mu        sync.Mutex
	capacity  int
	entries   map[int]*entry
	children  map[int]map[int]struct{}
	nextPID   int
	zombieSeq uint64
}

type entry struct {
	Entry
	zombieSeq uint64 // 成为僵尸的先后顺序号，WaitAny 按此回收最早者
}

// NewTable 创建容量为 capacity 的进程表，始进程（编号 1）已存在。
func NewTable(capacity int) *Table {
	if capacity < 1 {
		capacity = 1
	}
	t := &Table{
		capacity: capacity,
		entries:  make(map[int]*entry),
		children: make(map[int]map[int]struct{}),
		nextPID:  InitPID + 1,
	}
	t.entries[InitPID] = &entry{Entry: Entry{PID: InitPID, Parent: 0, Alive: true}}
	return t
}

// Create 在父进程 parentPID 下创建子进程，返回新进程编号。
//
// 拒绝顺序：父进程不存在（ErrNotExist）→ 父进程是僵尸（ErrZombie）→
// 表已满（ErrTableFull）。被拒绝时不改变任何状态。
func (t *Table) Create(parentPID int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	parent, ok := t.entries[parentPID]
	if !ok {
		return 0, fmt.Errorf("create: parent %d: %w", parentPID, ErrNotExist)
	}
	if !parent.Alive {
		return 0, fmt.Errorf("create: parent %d: %w", parentPID, ErrZombie)
	}
	if len(t.entries) >= t.capacity {
		return 0, fmt.Errorf("create: %w (capacity %d)", ErrTableFull, t.capacity)
	}
	pid := t.nextPID
	t.nextPID++
	t.entries[pid] = &entry{Entry: Entry{PID: pid, Parent: parentPID, Alive: true}}
	t.addChild(parentPID, pid)
	return pid, nil
}

// Exit 使进程 pid 以退出码 exitCode 退出。
//
// 退出后进程成为僵尸并保留表项与退出码；其全部子进程（存活的与僵尸的）
// 改挂到始进程，改挂后归始进程的僵尸立即被回收、表项释放；若退出者自己
// 的父进程就是始进程，它也立即被回收。
//
// 拒绝顺序：进程不存在（ErrNotExist）→ 已是僵尸（ErrZombie）→
// 始进程退出（ErrInitExit）。被拒绝时不改变任何状态。
func (t *Table) Exit(pid, exitCode int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[pid]
	if !ok {
		return fmt.Errorf("exit: pid %d: %w", pid, ErrNotExist)
	}
	if !e.Alive {
		return fmt.Errorf("exit: pid %d: %w", pid, ErrZombie)
	}
	if pid == InitPID {
		return fmt.Errorf("exit: pid %d: %w", pid, ErrInitExit)
	}
	e.Alive = false
	e.ExitCode = exitCode
	t.zombieSeq++
	e.zombieSeq = t.zombieSeq
	// 全部子进程改挂到始进程；归始进程的僵尸立即被回收。
	for childPID := range t.children[pid] {
		t.reparent(childPID, pid, InitPID)
		if child, ok := t.entries[childPID]; ok && !child.Alive {
			t.reapLocked(childPID)
		}
	}
	delete(t.children, pid) // 僵尸没有任何子进程
	// 退出者的父进程是始进程时，它自己也立即被回收。
	if e.Parent == InitPID {
		t.reapLocked(pid)
	}
	return nil
}

// Wait 等待指定子进程 childPID，回收僵尸并返回其退出码。
//
// 拒绝顺序：调用者不存在（ErrNotExist）→ 调用者是僵尸（ErrZombie）→
// 目标不存在（ErrNotExist）→ 目标不是自己的子进程（ErrNotChild）→
// 子进程仍存活（ErrChildAlive）。被拒绝时不改变任何状态。
func (t *Table) Wait(pid, childPID int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	caller, ok := t.entries[pid]
	if !ok {
		return 0, fmt.Errorf("wait: pid %d: %w", pid, ErrNotExist)
	}
	if !caller.Alive {
		return 0, fmt.Errorf("wait: pid %d: %w", pid, ErrZombie)
	}
	child, ok := t.entries[childPID]
	if !ok {
		return 0, fmt.Errorf("wait: target %d: %w", childPID, ErrNotExist)
	}
	if child.Parent != pid {
		return 0, fmt.Errorf("wait: target %d: %w", childPID, ErrNotChild)
	}
	if child.Alive {
		return 0, fmt.Errorf("wait: target %d: %w", childPID, ErrChildAlive)
	}
	code := child.ExitCode
	t.reapLocked(childPID)
	return code, nil
}

// WaitAny 等待任一子进程，回收最早成为僵尸者，返回其编号与退出码。
//
// 拒绝顺序：调用者不存在（ErrNotExist）→ 调用者是僵尸（ErrZombie）→
// 没有任何子进程（ErrNoChildren）→ 仍有存活子进程但暂无僵尸
// （ErrNoZombie）。被拒绝时不改变任何状态。
func (t *Table) WaitAny(pid int) (int, int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	caller, ok := t.entries[pid]
	if !ok {
		return 0, 0, fmt.Errorf("waitany: pid %d: %w", pid, ErrNotExist)
	}
	if !caller.Alive {
		return 0, 0, fmt.Errorf("waitany: pid %d: %w", pid, ErrZombie)
	}
	kids := t.children[pid]
	if len(kids) == 0 {
		return 0, 0, fmt.Errorf("waitany: pid %d: %w", pid, ErrNoChildren)
	}
	// 在子进程中找最早成为僵尸者。
	best := -1
	var bestSeq uint64
	for childPID := range kids {
		child := t.entries[childPID]
		if child.Alive {
			continue
		}
		if best == -1 || child.zombieSeq < bestSeq {
			best, bestSeq = childPID, child.zombieSeq
		}
	}
	if best == -1 {
		return 0, 0, fmt.Errorf("waitany: pid %d: %w", pid, ErrNoZombie)
	}
	code := t.entries[best].ExitCode
	t.reapLocked(best)
	return best, code, nil
}

// Len 返回当前表项数（含存活与僵尸），任何时刻不超过容量。
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// Capacity 返回进程表容量。
func (t *Table) Capacity() int {
	return t.capacity
}

// Lookup 查询进程表项快照；ok 为 false 表示进程不存在。
func (t *Table) Lookup(pid int) (e Entry, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ent, ok := t.entries[pid]
	if !ok {
		return Entry{}, false
	}
	return ent.Entry, true
}

// Children 返回进程 pid 当前的子进程编号（升序）。
func (t *Table) Children(pid int) []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	kids := t.children[pid]
	out := make([]int, 0, len(kids))
	for childPID := range kids {
		out = append(out, childPID)
	}
	sort.Ints(out)
	return out
}

// String 以确定性的文本形式描述整张表，便于日志与测试断言。
func (t *Table) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	pids := make([]int, 0, len(t.entries))
	for pid := range t.entries {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	var sb strings.Builder
	fmt.Fprintf(&sb, "table(%d/%d):", len(t.entries), t.capacity)
	for _, pid := range pids {
		e := t.entries[pid]
		state := "alive"
		if !e.Alive {
			state = fmt.Sprintf("zombie(code=%d)", e.ExitCode)
		}
		fmt.Fprintf(&sb, " [%d parent=%d %s]", e.PID, e.Parent, state)
	}
	return sb.String()
}

// addChild 把 childPID 登记为 parentPID 的子进程。调用方须持锁。
func (t *Table) addChild(parentPID, childPID int) {
	kids, ok := t.children[parentPID]
	if !ok {
		kids = make(map[int]struct{})
		t.children[parentPID] = kids
	}
	kids[childPID] = struct{}{}
}

// reparent 把 childPID 从 oldParent 改挂到 newParent。调用方须持锁。
func (t *Table) reparent(childPID, oldParent, newParent int) {
	if kids, ok := t.children[oldParent]; ok {
		delete(kids, childPID)
	}
	t.entries[childPID].Parent = newParent
	t.addChild(newParent, childPID)
}

// reapLocked 回收僵尸并释放表项。僵尸没有子进程，无需再改挂。
// 调用方须持锁，且每个僵尸至多被回收一次。
func (t *Table) reapLocked(pid int) {
	e := t.entries[pid]
	if kids, ok := t.children[e.Parent]; ok {
		delete(kids, pid)
	}
	delete(t.entries, pid)
}
