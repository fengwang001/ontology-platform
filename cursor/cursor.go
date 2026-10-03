// Package cursor 维护增量拉取器的复合游标 (ts,id) 与单调时钟 maxNow。
package cursor

import "sync"

// MaxNow 是 now/ts 允许的上界（ts、now 均不超过 10^12）。
const MaxNow = int64(1_000_000_000_000)

// Pos 是按 (ts,id) 字典序比较的复合位置。
type Pos struct {
	Ts int64
	ID int64
}

// Less 报告 p 是否严格小于 q。
func (p Pos) Less(q Pos) bool {
	if p.Ts != q.Ts {
		return p.Ts < q.Ts
	}
	return p.ID < q.ID
}

// Tracker 是游标状态机：游标单调不减，接受的 now 单调不减。
type Tracker struct {
	mu     sync.RWMutex
	cur    Pos
	maxNow int64
}

// New 创建初值 cur=(0,0)、maxNow=0 的 Tracker。
func New() *Tracker { return &Tracker{} }

// Cur 返回当前游标的快照副本。
func (t *Tracker) Cur() Pos {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cur
}

// MaxNow 返回已接受 Pull 的最大 now。
func (t *Tracker) MaxNow() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.maxNow
}

// BeginPull 校验 now 合法性与时钟单调性，计算本轮 hz 与回看起点。
// 拒绝次序：now 越界（参数非法）先于时钟回退；hz<0 时返回 skip=true。
// 无论返回什么都不修改状态（推进发生在 Commit）。
func (t *Tracker) BeginPull(now, d, b int64) (hz int64, start Pos, skip bool, err error) {
	if now < 0 || now > MaxNow {
		return 0, Pos{}, false, ErrInvalidParam
	}
	t.mu.RLock()
	rollback := now < t.maxNow
	cur := t.cur
	t.mu.RUnlock()
	if rollback {
		return 0, Pos{}, false, ErrClockRollback
	}
	hz = now - d
	if hz < 0 {
		return hz, Pos{}, true, nil
	}
	return hz, Lookback(cur, b), false, nil
}

// Commit 在整轮成功后推进 maxNow；last 非 nil 时游标取 max(cur,*last)。
func (t *Tracker) Commit(now int64, last *Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now > t.maxNow {
		t.maxNow = now
	}
	if last != nil && t.cur.Less(*last) {
		t.cur = *last
	}
}

// Lookback 返回以 cur 为当前游标时的回看起点 (max(cur.ts-b,0),0)。
func Lookback(cur Pos, b int64) Pos {
	ts := cur.Ts - b
	if ts < 0 {
		ts = 0
	}
	return Pos{Ts: ts, ID: 0}
}

// Advance 将游标推进到 max(cur,p)。
func (t *Tracker) Advance(p Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur.Less(p) {
		t.cur = p
	}
}
