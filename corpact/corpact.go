// Package corpact 登记公司行为、保存权益快照并记录执行结果。
package corpact

import "errors"

var (
	ErrInvalidParam = errors.New("corpact: invalid parameter")
	ErrDuplicateID  = errors.New("corpact: duplicate action id")
	ErrNotFound     = errors.New("corpact: action not found")
	ErrBadState     = errors.New("corpact: action state mismatch")
	ErrConflict     = errors.New("corpact: pending action conflict")
	ErrNoRefPrice   = errors.New("corpact: no reference close price")
)

// State 为行动的生命周期状态。
type State int

const (
	Announced State = iota
	Snapshotted
	Executed
	Cancelled
)

// SnapEntry 是快照中一个账户的登记日末持仓。
type SnapEntry struct {
	Acct []byte
	Q    int64
	F    int64
}

// AcctAward 是单个账户的除权所得。
type AcctAward struct {
	Acct       []byte
	Shares     int64
	SharesFroz int64
	Cash       int64
	CashFroz   int64
	FragCash   int64
}

// OrderAdj 是一条委托的调价结果。
type OrderAdj struct {
	OID      []byte
	NewPrice int64
	Canceled bool
}

// Result 是一次除权执行的完整结果。
type Result struct {
	Pex    int64
	Awards []AcctAward
	Orders []OrderAdj
}

// Action 是一条公司行为登记。
type Action struct {
	ID     []byte
	Sym    []byte
	Cash   int64
	Bonus  int64
	Rec    int64
	Ex     int64
	State  State
	Snap   []SnapEntry
	Result *Result
}

// Registry 自身不加锁，由上层 adjust.Engine 统一加锁。
type Registry struct {
	actions map[string]*Action
}

func NewRegistry() *Registry {
	return &Registry{actions: map[string]*Action{}}
}

// ActionsCount 返回已登记行动数（含已取消），主要用于测试对照。
func (r *Registry) ActionsCount() int { return len(r.actions) }

// Get 返回登记的副本指针；不存在返回 ErrNotFound。
func (r *Registry) Get(id []byte) (*Action, error) {
	a, ok := r.actions[string(id)]
	if !ok {
		return nil, ErrNotFound
	}
	return a, nil
}

// HasPending 报告该标的是否存在未执行（未取消）的行动。
func (r *Registry) HasPending(sym []byte) bool {
	for _, a := range r.actions {
		if string(a.Sym) == string(sym) && a.State != Cancelled && a.State != Executed {
			return true
		}
	}
	return false
}

// Announce 登记一条行动。调用方须保证参数合法；
// 重复 id 报 ErrDuplicateID；同标的已有未执行（未取消）行动报 ErrConflict。
func (r *Registry) Announce(id, sym []byte, cash, bonus, rec, ex int64) error {
	if _, ok := r.actions[string(id)]; ok {
		return ErrDuplicateID
	}
	for _, a := range r.actions {
		if string(a.Sym) == string(sym) && a.State != Cancelled && a.State != Executed {
			return ErrConflict
		}
	}
	r.actions[string(id)] = &Action{
		ID:    append([]byte(nil), id...),
		Sym:   append([]byte(nil), sym...),
		Cash:  cash,
		Bonus: bonus,
		Rec:   rec,
		Ex:    ex,
		State: Announced,
	}
	return nil
}

// Cancel 仅在快照生成之前允许撤销。
func (r *Registry) Cancel(id []byte) error {
	a, ok := r.actions[string(id)]
	if !ok {
		return ErrNotFound
	}
	if a.State != Announced {
		return ErrBadState
	}
	a.State = Cancelled
	return nil
}

// AttachSnap 写入登记日末快照并转入 Snapshotted 状态。
func (r *Registry) AttachSnap(id []byte, snap []SnapEntry) error {
	a, ok := r.actions[string(id)]
	if !ok {
		return ErrNotFound
	}
	if a.State != Announced {
		return ErrBadState
	}
	a.Snap = snap
	a.State = Snapshotted
	return nil
}

// AttachResult 写入执行结果并转入 Executed 状态。
func (r *Registry) AttachResult(id []byte, res *Result) error {
	a, ok := r.actions[string(id)]
	if !ok {
		return ErrNotFound
	}
	if a.State != Snapshotted {
		return ErrBadState
	}
	a.Result = res
	a.State = Executed
	return nil
}

// DueSnapshot 返回需要在“推进到 day 日”时生成快照的行动 id（rec<day 且尚未快照），按 id 字节序。
func (r *Registry) DueSnapshot(day int64) [][]byte {
	var ids []string
	for _, a := range r.actions {
		if a.State == Announced && a.Rec < day {
			ids = append(ids, string(a.ID))
		}
	}
	sortStrings(ids)
	return toBytes(ids)
}

// DueExecute 返回需要在“推进到 day 日”时执行的行动 id（ex<=day 且已快照未执行），按 id 字节序。
func (r *Registry) DueExecute(day int64) [][]byte {
	var ids []string
	for _, a := range r.actions {
		if a.State == Snapshotted && a.Ex <= day {
			ids = append(ids, string(a.ID))
		}
	}
	sortStrings(ids)
	return toBytes(ids)
}

func sortStrings(x []string) {
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j-1] > x[j]; j-- {
			x[j-1], x[j] = x[j], x[j-1]
		}
	}
}

func toBytes(ids []string) [][]byte {
	out := make([][]byte, len(ids))
	for i, s := range ids {
		out[i] = []byte(s)
	}
	return out
}
