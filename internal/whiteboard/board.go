package whiteboard

import (
	"math/rand/v2"
	"sync"
)

const (
	MaxGroupMembers = 200
	MinGroupMembers = 2
	MaxNow          = int64(1_000_000_000_000)
	MinTTL          = int64(1)
	MaxTTL          = int64(3600)
)

type elem struct {
	id      string
	group   string // 所属组合；空串表示未组合
	lastRev int64  // 最近一次影响该元素的修订号
}

type lockRec struct {
	holder   string
	expireAt int64
}

// Board 是并发安全的白板。零值不可用，请使用 New 创建。
type Board struct {
	mu     sync.Mutex
	order  *treap
	nodes  map[string]*node
	elems  map[string]*elem
	groups map[string]map[string]struct{}
	locks  map[string]lockRec
	rev    int64
	lastTs int64
	rng    *rand.Rand
}

// New 创建空白板，rev=0，自底向上序列为空。
func New() *Board {
	return &Board{
		order:  &treap{},
		nodes:  map[string]*node{},
		elems:  map[string]*elem{},
		groups: map[string]map[string]struct{}{},
		locks:  map[string]lockRec{},
		rng:    rand.New(rand.NewPCG(1, 2)),
	}
}

// Snapshot 是白板完整可比较状态，供测试与朴素模型对照。
type Snapshot struct {
	Order  []string
	Rev    int64
	LastTs int64
	Elems  map[string]ElemInfo
	Groups map[string][]string
	Locks  map[string]LockState
}

// ElemInfo 是元素的公开状态。
type ElemInfo struct {
	Group   string
	LastRev int64
}

// validUser 检查用户标识非空。
func validUser(user string) bool { return user != "" }

// validID 检查元素/组合标识非空。
func validID(id string) bool { return id != "" }

// validTime 检查逻辑时间在 [0, 10^12]。
func validTime(now int64) bool { return now >= 0 && now <= MaxNow }

// effectiveLock 返回 id 上当前有效的锁（含恰好到期视为无效），无则返回 nil。
// 调用方持锁。不做惰性删除，避免查询路径产生写入。
func (b *Board) effectiveLock(id string, now int64) *lockRec {
	if l, ok := b.locks[id]; ok && l.expireAt > now {
		return &l
	}
	return nil
}

// purgeExpired 删除 id 上已到期的锁记录（恰等于到期时刻删除）。
func (b *Board) purgeExpired(id string, now int64) {
	if l, ok := b.locks[id]; ok && l.expireAt <= now {
		delete(b.locks, id)
	}
}

// touchedLock 检查“触碰”一个元素是否被他人锁阻止：
// 元素自身锁或其所属组合锁由他人持有且未到期即拒绝。
// 返回需要报告的锁（目标 id、持有者、到期时刻）。
func (b *Board) touchedLock(elemID string, user string, now int64) *LockError {
	if l := b.effectiveLock(elemID, now); l != nil && l.holder != user {
		return &LockError{Holder: l.holder, ExpireAt: l.expireAt, Remain: l.expireAt - now, TargetID: elemID}
	}
	if g := b.elems[elemID].group; g != "" {
		if l := b.effectiveLock(g, now); l != nil && l.holder != user {
			return &LockError{Holder: l.holder, ExpireAt: l.expireAt, Remain: l.expireAt - now, TargetID: g}
		}
	}
	return nil
}
