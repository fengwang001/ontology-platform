// Package stagedcommit 提供带两阶段提交日志的暂存提交器。
//
// 键值变更先写入对读不可见的暂存区，再通过两阶段提交原子地
// 应用到可见视图：第一阶段把变更记入提交日志并标记为已准备
// （prepared），第二阶段应用到可见视图并把日志条目标记为已定稿
// （finalized）。若崩溃恰好发生在两步之间，日志中会留下一条
// 已准备未定稿的条目，恢复时将其判定为已废弃（aborted），
// 对可见视图零效果。
package stagedcommit

import (
	"errors"
	"sort"
	"sync"
)

// 三类互不相同的可判定错误，均可用 errors.Is 判定。
var (
	// ErrEmptyKey 表示写入了空键。
	ErrEmptyKey = errors.New("stagedcommit: empty key")
	// ErrEmptyCommit 表示暂存区为空时尝试提交。
	ErrEmptyCommit = errors.New("stagedcommit: empty commit")
	// ErrDeleteNotAllowed 表示删除目标不可删：键不在可见视图中，
	// 或已在暂存区被改动过。
	ErrDeleteNotAllowed = errors.New("stagedcommit: delete target not deletable")
	// ErrSimulatedCrash 表示提交在两阶段之间发生模拟崩溃：
	// 日志已留下已准备未定稿条目，视图未变，暂存区已清空，
	// 提交号已消耗。
	ErrSimulatedCrash = errors.New("stagedcommit: simulated crash between prepare and finalize")
)

// EntryState 是提交日志条目的状态。
type EntryState string

const (
	// StatePrepared 已准备：第一阶段完成，尚未应用到视图。
	StatePrepared EntryState = "prepared"
	// StateFinalized 已定稿：第二阶段完成，变更已可见。
	StateFinalized EntryState = "finalized"
	// StateAborted 已废弃：恢复时被判定为从未发生。
	StateAborted EntryState = "aborted"
)

// opKind 是暂存区变更的种类。
type opKind int

const (
	opPut opKind = iota
	opDelete
)

// op 是暂存区中的一条变更。
type op struct {
	kind  opKind
	value string
}

// Change 是提交日志中记录的一条变更，供日志与测试检视。
type Change struct {
	Key    string
	Delete bool
	Value  string
}

// LogEntry 是提交日志中的一条记录。
type LogEntry struct {
	Number  uint64
	State   EntryState
	Changes []Change
}

// View 是可见视图在某一完整提交边界上的不可变快照。
type View struct {
	// Commit 是产生该视图的最近一次已定稿提交号，初始为 0。
	Commit uint64
	Data   map[string]string
}

// Committer 是带两阶段提交日志的暂存提交器。
// 所有方法均可并发调用；读到的视图必然对应某个完整提交边界。
type Committer struct {
	mu         sync.RWMutex
	staging    map[string]op
	view       map[string]string
	viewCommit uint64
	log        []LogEntry
	nextCommit uint64
	crashArmed bool
}

// New 返回一个空的提交器，提交号从 1 开始递增。
func New() *Committer {
	return &Committer{
		staging:    make(map[string]op),
		view:       make(map[string]string),
		nextCommit: 1,
	}
}

// Put 把键值写入暂存区，覆盖同键的既有暂存变更；对读不可见。
func (c *Committer) Put(key, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staging[key] = op{kind: opPut, value: value}
	return nil
}

// Delete 在暂存区登记删除。仅当键当前在可见视图存在且尚未在
// 暂存区被改动时允许，否则返回 ErrDeleteNotAllowed。
func (c *Committer) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, staged := c.staging[key]; staged {
		return ErrDeleteNotAllowed
	}
	if _, visible := c.view[key]; !visible {
		return ErrDeleteNotAllowed
	}
	c.staging[key] = op{kind: opDelete}
	return nil
}

// Commit 执行两阶段提交：先把暂存变更写入日志记为已准备，
// 再应用到可见视图并标记已定稿。成功后暂存区清空，返回提交号。
// 暂存区为空时返回 ErrEmptyCommit，状态不变。
func (c *Committer) Commit() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.staging) == 0 {
		return 0, ErrEmptyCommit
	}

	number := c.nextCommit
	c.nextCommit++

	// 第一阶段：把变更写入提交日志，记为已准备。
	entry := LogEntry{Number: number, State: StatePrepared, Changes: changesOf(c.staging)}
	c.log = append(c.log, entry)

	// 崩溃点：模拟崩溃恰好发生在两步之间。日志已留下已准备
	// 未定稿的条目，视图不变，暂存区照常清空，提交号照常消耗。
	if c.crashArmed {
		c.crashArmed = false
		c.staging = make(map[string]op)
		return number, ErrSimulatedCrash
	}

	// 第二阶段：应用到可见视图并把日志条目标记为已定稿。
	c.applyLocked(entry.Changes)
	c.log[len(c.log)-1].State = StateFinalized
	c.staging = make(map[string]op)
	return number, nil
}

// ArmCrash 武装一次模拟崩溃：下一次 Commit 会在两阶段之间崩溃，
// 日志留下已准备未定稿条目，视图不变，暂存区照常清空，
// 提交号照常消耗，Commit 返回 ErrSimulatedCrash。
func (c *Committer) ArmCrash() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.crashArmed = true
}

// Recover 把最新一条已准备未定稿的日志条目判定为已废弃
// （视为从未发生）并返回其提交号；没有待判定条目时返回 0。
func (c *Committer) Recover() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.log) - 1; i >= 0; i-- {
		if c.log[i].State == StatePrepared {
			c.log[i].State = StateAborted
			return c.log[i].Number
		}
	}
	return 0
}

// Rollback 清空暂存区，永远成功且无痕迹。
func (c *Committer) Rollback() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staging = make(map[string]op)
	return nil
}

// Snapshot 返回可见视图在当前完整提交边界上的不可变快照。
func (c *Committer) Snapshot() View {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data := make(map[string]string, len(c.view))
	for k, v := range c.view {
		data[k] = v
	}
	return View{Commit: c.viewCommit, Data: data}
}

// StagingSnapshot 返回暂存区变更的只读快照，按键排序，供检视。
func (c *Committer) StagingSnapshot() []Change {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return changesOf(c.staging)
}

// Log 返回提交日志的只读副本。
func (c *Committer) Log() []LogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]LogEntry, len(c.log))
	for i, e := range c.log {
		changes := make([]Change, len(e.Changes))
		copy(changes, e.Changes)
		out[i] = LogEntry{Number: e.Number, State: e.State, Changes: changes}
	}
	return out
}

// NextCommitNumber 返回下一次提交将消耗的提交号。
func (c *Committer) NextCommitNumber() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nextCommit
}

// applyLocked 把一批变更应用到可见视图并推进视图提交号。
// 调用方必须持有写锁。
func (c *Committer) applyLocked(changes []Change) {
	for _, ch := range changes {
		if ch.Delete {
			delete(c.view, ch.Key)
		} else {
			c.view[ch.Key] = ch.Value
		}
	}
	c.viewCommit = c.log[len(c.log)-1].Number
}

// changesOf 把暂存区变更转换为按键排序的日志变更列表。
func changesOf(staging map[string]op) []Change {
	keys := make([]string, 0, len(staging))
	for k := range staging {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	changes := make([]Change, 0, len(keys))
	for _, k := range keys {
		o := staging[k]
		changes = append(changes, Change{
			Key:    k,
			Delete: o.kind == opDelete,
			Value:  o.value,
		})
	}
	return changes
}
