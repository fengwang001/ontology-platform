// Package proctable 实现带僵尸回收的进程表。
//
// 进程表容量为 N（含存活与僵尸）。始进程编号为 1，建表时已存在且永不退出。
// 进程退出后成为僵尸，保留表项与退出码；其全部子进程（存活与僵尸）改挂到
// 始进程，改挂后归始进程的僵尸立即被回收；若退出者自己的父进程就是始进程，
// 它也立即被回收。父进程可等待指定子进程或任一子进程以回收僵尸。
package proctable

import (
	"fmt"
	"sort"
	"sync"
)

// InitPID 是始进程的编号，建表时已存在且永不退出。
const InitPID = 1

// Kind 区分可判定的拒绝原因。
type Kind int

const (
	// ErrNotFound 目标进程不存在（不在表中）。
	ErrNotFound Kind = iota
	// ErrZombie 目标是僵尸，不能再对其做创建、退出或等待。
	ErrZombie
	// ErrTableFull 表已满（存活与僵尸合计达到容量）。
	ErrTableFull
	// ErrInitExit 始进程不允许退出。
	ErrInitExit
	// ErrNotChild 等待的目标不是自己的子进程。
	ErrNotChild
	// ErrStillAlive 指定等待的子进程仍存活。
	ErrStillAlive
	// ErrNoChildren 等待任一子进程时没有任何子进程。
	ErrNoChildren
	// ErrNoZombie 等待任一子进程时仍有存活子进程但无僵尸。
	ErrNoZombie
)

// Error 是一次被拒绝操作的原因，整体拒绝且不改变任何状态。
type Error struct {
	Kind Kind
	Op   string
	PID  int
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s(pid=%d): %s", e.Op, e.PID, e.Msg)
}

func (k Kind) String() string {
	switch k {
	case ErrNotFound:
		return "not-found"
	case ErrZombie:
		return "zombie"
	case ErrTableFull:
		return "table-full"
	case ErrInitExit:
		return "init-exit"
	case ErrNotChild:
		return "not-child"
	case ErrStillAlive:
		return "still-alive"
	case ErrNoChildren:
		return "no-children"
	case ErrNoZombie:
		return "no-zombie"
	default:
		return "unknown"
	}
}

// Entry 是一个表项的只读快照。
type Entry struct {
	PID      int
	Parent   int // 始进程的父进程为 0
	Alive    bool
	ExitCode int // 仅僵尸有效
}

// Table 是并发安全的进程表。所有操作在同一把互斥锁下串行化，
// 相同操作序列重放得到完全相同的表与回收顺序。
type Table struct {
	mu        sync.Mutex
	cap       int
	entries   map[int]*entry
	nextPID   int
	zombieSeq int64
	reaped    []int
}

type entry struct {
	pid      int
	parent   int
	alive    bool
	exitCode int
	zseq     int64
	children map[int]bool
}

// New 创建容量为 capacity 的进程表，并放入始进程（编号 1）。
func New(capacity int) *Table {
	if capacity < 1 {
		capacity = 1
	}
	t := &Table{
		cap:     capacity,
		entries: make(map[int]*entry),
		nextPID: InitPID + 1,
	}
	t.entries[InitPID] = &entry{pid: InitPID, alive: true, children: make(map[int]bool)}
	return t
}

// Create 在 parent 下创建子进程，返回自 2 起严格递增、不复用的新编号。
func (t *Table) Create(parent int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.entries[parent]
	if !ok {
		return 0, &Error{Kind: ErrNotFound, Op: "create", PID: parent, Msg: "父进程不存在"}
	}
	if !p.alive {
		return 0, &Error{Kind: ErrZombie, Op: "create", PID: parent, Msg: "父进程已是僵尸"}
	}
	if len(t.entries) >= t.cap {
		return 0, &Error{Kind: ErrTableFull, Op: "create", PID: parent, Msg: "进程表已满"}
	}
	pid := t.nextPID
	t.nextPID++
	t.entries[pid] = &entry{pid: pid, parent: parent, alive: true, children: make(map[int]bool)}
	p.children[pid] = true
	return pid, nil
}

// Exit 使 pid 退出并成为僵尸，保留退出码；按规则改挂子进程并立即回收。
func (t *Table) Exit(pid, code int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[pid]
	if !ok {
		return &Error{Kind: ErrNotFound, Op: "exit", PID: pid, Msg: "进程不存在"}
	}
	if !e.alive {
		return &Error{Kind: ErrZombie, Op: "exit", PID: pid, Msg: "进程已是僵尸"}
	}
	if pid == InitPID {
		return &Error{Kind: ErrInitExit, Op: "exit", PID: pid, Msg: "始进程不允许退出"}
	}
	e.alive = false
	e.exitCode = code
	t.zombieSeq++
	e.zseq = t.zombieSeq
	init := t.entries[InitPID]
	for _, cid := range sortedKeys(e.children) {
		c := t.entries[cid]
		c.parent = InitPID
		init.children[cid] = true
		if !c.alive {
			t.reap(c)
		}
	}
	e.children = make(map[int]bool)
	if e.parent == InitPID {
		t.reap(e)
	}
	return nil
}

// Wait 等待指定子进程 child：其为僵尸则回收并返回退出码。
func (t *Table) Wait(parent, child int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.entries[parent]
	if !ok {
		return 0, &Error{Kind: ErrNotFound, Op: "wait", PID: parent, Msg: "父进程不存在"}
	}
	c, ok := t.entries[child]
	if !ok {
		return 0, &Error{Kind: ErrNotFound, Op: "wait", PID: child, Msg: "目标进程不存在"}
	}
	if !p.alive {
		return 0, &Error{Kind: ErrZombie, Op: "wait", PID: parent, Msg: "父进程已是僵尸"}
	}
	if !p.children[child] {
		return 0, &Error{Kind: ErrNotChild, Op: "wait", PID: child, Msg: "目标不是自己的子进程"}
	}
	if c.alive {
		return 0, &Error{Kind: ErrStillAlive, Op: "wait", PID: child, Msg: "指定子进程仍存活"}
	}
	code := c.exitCode
	t.reap(c)
	return code, nil
}

// WaitAny 等待任一子进程：回收最早成为僵尸者，返回其编号与退出码。
func (t *Table) WaitAny(parent int) (int, int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.entries[parent]
	if !ok {
		return 0, 0, &Error{Kind: ErrNotFound, Op: "wait-any", PID: parent, Msg: "父进程不存在"}
	}
	if !p.alive {
		return 0, 0, &Error{Kind: ErrZombie, Op: "wait-any", PID: parent, Msg: "父进程已是僵尸"}
	}
	if len(p.children) == 0 {
		return 0, 0, &Error{Kind: ErrNoChildren, Op: "wait-any", PID: parent, Msg: "没有任何子进程"}
	}
	var oldest *entry
	for _, cid := range sortedKeys(p.children) {
		c := t.entries[cid]
		if c.alive {
			continue
		}
		if oldest == nil || c.zseq < oldest.zseq {
			oldest = c
		}
	}
	if oldest == nil {
		return 0, 0, &Error{Kind: ErrNoZombie, Op: "wait-any", PID: parent, Msg: "仍有存活子进程，无僵尸可回收"}
	}
	code := oldest.exitCode
	pid := oldest.pid
	t.reap(oldest)
	return pid, code, nil
}

// Lookup 返回 pid 的表项快照；不存在时 ok 为 false。
func (t *Table) Lookup(pid int) (Entry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[pid]
	if !ok {
		return Entry{}, false
	}
	return Entry{PID: e.pid, Parent: e.parent, Alive: e.alive, ExitCode: e.exitCode}, true
}

// Children 返回 pid 的子进程编号（升序）；pid 不存在时返回 nil。
func (t *Table) Children(pid int) []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[pid]
	if !ok {
		return nil
	}
	return sortedKeys(e.children)
}

// Size 返回当前表项数（存活与僵尸合计），恒不超过容量。
func (t *Table) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// Reaped 返回至今为止被回收的进程编号序列（按回收先后）。
func (t *Table) Reaped() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]int, len(t.reaped))
	copy(out, t.reaped)
	return out
}

// reap 回收一个僵尸表项：从父进程的子进程集合移除、释放表项并记录回收顺序。
// 调用方须保证 e 是僵尸（僵尸没有子进程，故无需级联处理）。
func (t *Table) reap(e *entry) {
	if p, ok := t.entries[e.parent]; ok {
		delete(p.children, e.pid)
	}
	delete(t.entries, e.pid)
	t.reaped = append(t.reaped, e.pid)
}

func sortedKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
