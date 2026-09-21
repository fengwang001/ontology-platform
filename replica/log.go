package replica

import (
	"errors"
	"fmt"
	"sync"
)

// Entry 是日志中的一条记录。
type Entry struct {
	Index uint64
	Term  uint64
	Data  string
}

var (
	// ErrIndexGap 表示追加的 Index 与当前日志不连续（跳号或回退）。
	ErrIndexGap = errors.New("replica: append index must be contiguous")
	// ErrTermRollback 表示追加条目的 Term 小于前一条，日志不允许在同一尾部回退 Term。
	ErrTermRollback = errors.New("replica: term must not roll back")
)

// Replica 是单个副本的本地持久化日志（内存模拟），方法均为并发安全。
type Replica struct {
	mu      sync.RWMutex
	id      int
	entries []Entry
	// next 是下一条应被追加的 Index；entries 始终是 [first, next) 的稠密序列。
	first uint64
	next  uint64
}

// New 创建一个空日志副本，日志下标从 1 开始。
func New(id int) *Replica {
	return &Replica{id: id, first: 1, next: 1}
}

// ID 返回副本编号。
func (r *Replica) ID() int {
	return r.id
}

// Append 在日志尾部追加一条记录。
// Index 必须等于 Match()+1（不得跳号或回退），Term 不得小于最后一条的 Term。
func (r *Replica) Append(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if e.Index != r.next {
		return fmt.Errorf("%w: got %d, want %d", ErrIndexGap, e.Index, r.next)
	}
	if len(r.entries) > 0 {
		if prev := r.entries[len(r.entries)-1].Term; e.Term < prev {
			return fmt.Errorf("%w: got %d, prev %d", ErrTermRollback, e.Term, prev)
		}
	}
	r.entries = append(r.entries, e)
	r.next++
	return nil
}

// Match 返回已持久化的最高 Index；空日志返回 0。
func (r *Replica) Match() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.matchLocked()
}

func (r *Replica) matchLocked() uint64 {
	if len(r.entries) == 0 {
		return 0
	}
	return r.entries[len(r.entries)-1].Index
}

// Get 返回指定 Index 的条目；不存在时 ok 为 false。
func (r *Replica) Get(i uint64) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if i < r.first || i >= r.next {
		return Entry{}, false
	}
	return r.entries[i-r.first], true
}
