// Package raft 实现副本状态、日志复制的截断与追加、提交判定。依赖 entry。
package raft

import (
	"errors"

	"ontology/entry"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrEmptyCmd         = errors.New("raft: empty command")
	ErrPrevIndexRange   = errors.New("raft: prevIndex out of range")
	ErrPrevTermMismatch = errors.New("raft: prev term mismatch")
)

// Replica 是一个副本的内存状态。Term 为当前任期；其余字段非导出。
type Replica struct {
	Term        int
	log         []entry.Entry
	commitIndex int
	checked     int // 最近一次 CommitIndex 检查过的日志条目个数
}

// Log 返回日志副本，调用方修改不影响内部状态。
func (r *Replica) Log() []entry.Entry {
	out := make([]entry.Entry, len(r.log))
	copy(out, r.log)
	return out
}

// Committed 返回当前已提交的 commitIndex。
func (r *Replica) Committed() int { return r.commitIndex }

// Append 以当前任期追加一条命令；空命令被拒且状态不变。
func (r *Replica) Append(cmd string) (entry.Entry, error) {
	if cmd == "" {
		return entry.Entry{}, ErrEmptyCmd
	}
	e := entry.Entry{Term: r.Term, Index: len(r.log) + 1, Cmd: cmd}
	r.log = entry.Append(r.log, e)
	return e, nil
}

// Replicate 把 leader 的 log[prevIndex+1..] 复制给 follower。
// 前缀不匹配或 prevIndex 越界时 follower 完全不变，返回哨兵错误。
func Replicate(leader, follower *Replica, prevIndex int) error {
	if prevIndex < 0 || prevIndex > len(leader.log) || prevIndex > len(follower.log) {
		return ErrPrevIndexRange
	}
	prevTerm := 0
	if prevIndex > 0 {
		prevTerm = leader.log[prevIndex-1].Term
	}
	if !entry.Match(follower.log, prevIndex, prevTerm) {
		return ErrPrevTermMismatch
	}
	follower.log = entry.Truncate(follower.log, prevIndex)
	follower.log = entry.Append(follower.log, leader.log[prevIndex:]...)
	return nil
}

// CommitIndex 从上次的 commitIndex 之后增量检查：返回最大的 index，
// 使该处条目在多数派上以相同 term 存在且该 term == leader 当前任期。
// all 必须包含 leader 自身。
func CommitIndex(leader *Replica, all []*Replica) int {
	checked := 0
	for i := leader.commitIndex + 1; i <= len(leader.log); i++ {
		checked++
		t := leader.log[i-1].Term
		n := 0
		for _, r := range all {
			if e, ok := entry.At(r.log, i); ok && e.Term == t {
				n++
			}
		}
		if n >= len(all)/2+1 && t == leader.Term {
			leader.commitIndex = i
		}
	}
	leader.checked = checked
	return leader.commitIndex
}
