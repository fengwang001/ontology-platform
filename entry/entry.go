// Package entry 实现单个缓存条目的状态机与版本绑定。
//
// 状态机：Hole(空洞，回源尚未发生) -> Valid(有效，含负缓存)
// -> Stale(失效待回源)；Loading(回源中)由外层协调器根据
// 单飞状态推导，不作为持久状态存储。
package entry

import (
	"time"

	"ontology/version"
)

// State 是条目的状态。
type State int

const (
	// Hole 空洞：回源尚未发生（或条目刚被接纳、上次回源失败）。
	Hole State = iota
	// Valid 有效：持有数据（含负缓存）且未失效。
	Valid
	// Stale 失效待回源：收到过更新版本的失效通知或已过期。
	Stale
	// Loading 回源中：仅用于对外报告，由单飞状态推导。
	Loading
)

// String 返回状态的可读名称。
func (s State) String() string {
	switch s {
	case Hole:
		return "Hole"
	case Valid:
		return "Valid"
	case Stale:
		return "Stale"
	case Loading:
		return "Loading"
	default:
		return "Unknown"
	}
}

// Entry 是单个缓存条目。非并发安全，由持有方（replica）加锁保护。
type Entry struct {
	key        string
	state      State
	value      string
	found      bool // 负缓存：false 表示缓存的是"键不存在"这一事实
	ver        version.Version
	invVer     version.Version // 已应用的最高失效通知版本
	expiresAt  time.Duration
	lastAccess uint64 // LRU 序号，由持有方分配的单调序号

	hits          uint64
	misses        uint64
	refetches     uint64
	invalidations uint64
}

// New 创建一个空洞条目。
func New(key string) *Entry {
	return &Entry{key: key, state: Hole}
}

// Key 返回条目键。
func (e *Entry) Key() string { return e.key }

// State 返回原始状态（不做惰性过期推导）。
func (e *Entry) State() State { return e.state }

// EffectiveState 返回考虑惰性过期后的状态：
// Valid 且 now >= expiresAt 时视为 Stale（左闭右开）。
func (e *Entry) EffectiveState(now time.Duration) State {
	if e.state == Valid && now >= e.expiresAt {
		return Stale
	}
	return e.state
}

// Alive 报告条目是否有效且存活：now 严格小于到期时刻才算存活。
func (e *Entry) Alive(now time.Duration) bool {
	return e.state == Valid && now < e.expiresAt
}

// Value 返回缓存的值。
func (e *Entry) Value() string { return e.value }

// Found 报告缓存的是否为"存在"的数据（false 为负缓存）。
func (e *Entry) Found() bool { return e.found }

// Version 返回当前持有数据的版本。
func (e *Entry) Version() version.Version { return e.ver }

// InvalidatedVersion 返回已应用的最高失效通知版本。
func (e *Entry) InvalidatedVersion() version.Version { return e.invVer }

// Floor 返回版本下界：持有版本与已应用失效版本的较大者。
// 任何版本号不超过 Floor 的通知都必须丢弃，任何版本号低于
// Floor 的回源结果都必须丢弃。
func (e *Entry) Floor() version.Version { return version.Max(e.ver, e.invVer) }

// ExpiresAt 返回到期时刻。
func (e *Entry) ExpiresAt() time.Duration { return e.expiresAt }

// TTLRemaining 返回剩余存活时长，非存活时返回 0。
func (e *Entry) TTLRemaining(now time.Duration) time.Duration {
	if !e.Alive(now) {
		return 0
	}
	return e.expiresAt - now
}

// ApplyInvalidate 应用一条失效通知。
// 版本不超过 Floor 的通知被丢弃并返回 false（幂等、防倒退）；
// 生效时记录失效版本、把 Valid 降级为 Stale 并返回 true。
func (e *Entry) ApplyInvalidate(v version.Version) bool {
	if !v.After(e.Floor()) {
		return false
	}
	e.invVer = v
	if e.state == Valid {
		e.state = Stale
	}
	e.invalidations++
	return true
}

// Fill 用回源结果填充条目，进入 Valid 状态。
func (e *Entry) Fill(value string, found bool, v version.Version, expiresAt time.Duration) {
	e.value = value
	e.found = found
	e.ver = v
	e.expiresAt = expiresAt
	e.state = Valid
	e.refetches++
}

// Touch 更新 LRU 序号。
func (e *Entry) Touch(seq uint64) { e.lastAccess = seq }

// LastAccess 返回 LRU 序号。
func (e *Entry) LastAccess() uint64 { return e.lastAccess }

// IncHits 增加命中计数。
func (e *Entry) IncHits() { e.hits++ }

// IncMisses 增加未命中计数。
func (e *Entry) IncMisses() { e.misses++ }

// UndoMiss 撤销一次未命中计数（用于超限拒绝的回滚）。
func (e *Entry) UndoMiss() {
	if e.misses > 0 {
		e.misses--
	}
}

// Stats 返回条目的只读计数快照。
type Stats struct {
	Hits          uint64
	Misses        uint64
	Refetches     uint64
	Invalidations uint64
}

// Stats 返回条目计数快照。
func (e *Entry) Stats() Stats {
	return Stats{
		Hits:          e.hits,
		Misses:        e.misses,
		Refetches:     e.refetches,
		Invalidations: e.invalidations,
	}
}
