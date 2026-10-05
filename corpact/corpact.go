// Package corpact 维护公司行为登记与日程：交易日历、收盘价、
// 行动登记（Announce/CancelAction）、权益登记快照与结果登记（Result）。
package corpact

import (
	"sort"
	"sync"

	"ontology/holding"
)

const (
	// MaxDay 为交易日上限（含）。
	MaxDay = 1_000_000
	// MaxCash 为每 10 股派现上限（分，含）。
	MaxCash = int64(1_000_000)
	// MaxBonus 为每 10 股送股上限（含）。
	MaxBonus = int64(100)
	// MaxClose 为收盘价上限（分，含）。
	MaxClose = int64(1_000_000_000)
)

// Snap 为权益登记快照中一个账户的持仓。
type Snap struct {
	Q int64
	F int64
}

// AccountGain 为执行时一个账户获得的权益。
type AccountGain struct {
	Acct         string
	Shares       int64 // 送股总数 n
	FrozenShares int64 // 其中记入冻结 nf
	Cash         int64 // 现金红利 m
	FrozenCash   int64 // 其中冻结现金 mf
	FractionCash int64 // 碎股折现（计入可用现金）
}

// OrderAdj 为一条在簿委托的调价记录。
type OrderAdj struct {
	Oid       string
	OldPrice  int64
	NewPrice  int64
	Cancelled bool // 新价小于 1 的买单被撤销
}

// Result 为一次除权执行的结果。
type Result struct {
	Pex    int64
	Gains  []AccountGain // 按账户字节序
	Orders []OrderAdj    // 按 oid 字节序
}

// Action 为一条公司行为登记。
type Action struct {
	ID  string
	Sym string
	C   int64 // 每 10 股派现（分）
	B   int64 // 每 10 股送股
	Rec int   // 股权登记日
	Ex  int   // 除权除息日

	Snapshotted bool
	Snapshot    map[string]Snap // 仅 q>0 的账户
	Executed    bool
	Res         *Result
}

// Registry 为公司行为登记处，所有方法可并发调用。
type Registry struct {
	mu           sync.Mutex
	book         *holding.Book
	day          int
	closes       map[string]int64
	actions      map[string]*Action
	pendingBySym map[string]string // 标的 -> 未执行行动 id
}

// New 创建登记处，快照通过 book 采集。
func New(book *holding.Book) *Registry {
	return &Registry{
		book:         book,
		closes:       make(map[string]int64),
		actions:      make(map[string]*Action),
		pendingBySym: make(map[string]string),
	}
}

// Day 返回当前交易日。
func (r *Registry) Day() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.day
}

// SetClose 记收盘价，P 取值 1..1e9（分）。
func (r *Registry) SetClose(sym string, p int64) error {
	if sym == "" || p < 1 || p > MaxClose {
		return holding.ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes[sym] = p
	return nil
}

// Close 返回该标的最近一次收盘价。
func (r *Registry) Close(sym string) (int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.closes[sym]
	return p, ok
}

// Announce 登记公司行为：每 10 股派现 c 分、送 b 股，rec 为登记日，ex 为除权日。
// 拒绝次序：参数非法 > 编号重复 > 状态不符(rec<=当前日) > 冲突 > 无参考价。
func (r *Registry) Announce(id, sym string, c, b int64, rec, ex int) error {
	if id == "" || sym == "" ||
		c < 0 || c > MaxCash || b < 0 || b > MaxBonus || (c == 0 && b == 0) ||
		rec < 0 || rec >= ex || ex > MaxDay {
		return holding.ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.actions[id]; ok {
		return holding.ErrDuplicate
	}
	if rec <= r.day {
		return holding.ErrState
	}
	if _, ok := r.pendingBySym[sym]; ok {
		return holding.ErrConflict
	}
	if _, ok := r.closes[sym]; !ok {
		return holding.ErrNoRefPrice
	}
	r.actions[id] = &Action{ID: id, Sym: sym, C: c, B: b, Rec: rec, Ex: ex}
	r.pendingBySym[sym] = id
	return nil
}

// CancelAction 撤销登记，仅允许在快照之前。
func (r *Registry) CancelAction(id string) error {
	if id == "" {
		return holding.ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.actions[id]
	if !ok {
		return holding.ErrNotExist
	}
	if a.Snapshotted || a.Executed {
		return holding.ErrState
	}
	delete(r.actions, id)
	delete(r.pendingBySym, a.Sym)
	return nil
}

// Advance 把当前日推进到 d（0<=d<=1e6 且不得回退）。
// 先为所有首次满足 day>rec 的行动拍快照（即 rec 日末状态），
// 再返回所有首次满足 day>=ex 的待执行行动 id（按 id 字节序）。
func (r *Registry) Advance(d int) ([]string, error) {
	if d < 0 || d > MaxDay {
		return nil, holding.ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if d < r.day {
		return nil, holding.ErrDateRollback
	}
	r.day = d
	ids := make([]string, 0, len(r.actions))
	for id := range r.actions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var exec []string
	for _, id := range ids {
		a := r.actions[id]
		if !a.Snapshotted && d > a.Rec {
			raw := r.book.SnapshotSymbol(a.Sym)
			snap := make(map[string]Snap, len(raw))
			for acct, p := range raw {
				snap[acct] = Snap{Q: p.Q, F: p.F}
			}
			a.Snapshot = snap
			a.Snapshotted = true
		}
		if !a.Executed && d >= a.Ex {
			exec = append(exec, id)
		}
	}
	return exec, nil
}

// GetAction 返回行动记录；不存在时 ok=false。
func (r *Registry) GetAction(id string) (*Action, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.actions[id]
	return a, ok
}

// RecordResult 登记执行结果：标记已执行，该标的收盘价记为 Pex，解除标的占用。
func (r *Registry) RecordResult(id string, res *Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.actions[id]
	if !ok {
		return
	}
	a.Executed = true
	a.Res = res
	r.closes[a.Sym] = res.Pex
	delete(r.pendingBySym, a.Sym)
}

// Result 返回已执行行动的结果；未执行时报状态不符。
func (r *Registry) Result(id string) (*Result, error) {
	if id == "" {
		return nil, holding.ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.actions[id]
	if !ok {
		return nil, holding.ErrNotExist
	}
	if !a.Executed {
		return nil, holding.ErrState
	}
	out := &Result{Pex: a.Res.Pex}
	if a.Res.Gains != nil {
		out.Gains = append([]AccountGain(nil), a.Res.Gains...)
	}
	if a.Res.Orders != nil {
		out.Orders = append([]OrderAdj(nil), a.Res.Orders...)
	}
	return out, nil
}
