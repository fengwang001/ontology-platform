// Package rule 持有规则更新类型、按版本顺序排列的发布日志、规则集快照与阈值命中判定。
// 它不依赖其他包。
package rule

import (
	"errors"
	"sort"
)

// Put 新增或覆盖一条阈值规则；Delete 删除一条规则。
type Put struct {
	ID        string
	Threshold int64
}

type Delete struct{ ID string }

// Update 是一次发布的内容，只能是 Put 或 Delete。
type Update interface{ ruleUpdate() }

func (Put) ruleUpdate()    {}
func (Delete) ruleUpdate() {}

// ErrIllegalUpdate: 规则 ID 为空，或 Delete 一条当前全局规则集中不存在的规则。
var ErrIllegalUpdate = errors.New("rule: illegal update")

// Set 是某一版本时刻的规则集：ID -> 阈值。
type Set map[string]int64

// Match 返回规则集中所有满足 val >= Threshold 的规则 ID，按字典序排列。
func (s Set) Match(val int64) []string {
	ids := make([]string, 0, len(s))
	for id, th := range s {
		if val >= th {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Apply 就地把一次更新作用于规则集。
func (s Set) Apply(u Update) {
	switch x := u.(type) {
	case Put:
		s[x.ID] = x.Threshold
	case Delete:
		delete(s, x.ID)
	default:
		panic("rule: unknown update")
	}
}

// Clone 返回规则集的独立副本。
func (s Set) Clone() Set {
	c := make(Set, len(s))
	for id, th := range s {
		c[id] = th
	}
	return c
}

// Log 是全局发布日志，版本号从 1 开始：版本 v 对应 At(v)。
type Log struct {
	entries []Update
	cur     Set
}

// NewLog 创建空日志（G=0，规则集为空）。
func NewLog() *Log { return &Log{cur: Set{}} }

// Len 返回当前全局版本 G。
func (l *Log) Len() int { return len(l.entries) }

// At 返回版本 ver（1 起算）对应的更新。
func (l *Log) At(ver int) Update { return l.entries[ver-1] }

// Entries 返回日志副本。
func (l *Log) Entries() []Update {
	out := make([]Update, len(l.entries))
	copy(out, l.entries)
	return out
}

// Snapshot 返回当前全局规则集的独立副本。
func (l *Log) Snapshot() Set { return l.cur.Clone() }

// SnapshotAt 从空集重放前 ver 个版本，返回该版本时刻的规则集。
func (l *Log) SnapshotAt(ver int) Set {
	s := Set{}
	for k := 0; k < ver; k++ {
		s.Apply(l.entries[k])
	}
	return s
}

// Append 校验后使全局版本加 1；非法更新不改任何状态。
func (l *Log) Append(u Update) error {
	id := ""
	switch x := u.(type) {
	case Put:
		id = x.ID
	case Delete:
		id = x.ID
		if _, ok := l.cur[x.ID]; !ok {
			return ErrIllegalUpdate
		}
	default:
		return ErrIllegalUpdate
	}
	if id == "" {
		return ErrIllegalUpdate
	}
	l.cur.Apply(u)
	l.entries = append(l.entries, u)
	return nil
}
