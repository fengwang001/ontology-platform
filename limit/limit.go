// Package limit 持有账户分组与合约限额表。
package limit

import "errors"

// ErrDuplicate 表示账户重复登记。
var ErrDuplicate = errors.New("duplicate registration")

// Side 为持仓方向。
type Side int

const (
	Long Side = iota + 1
	Short
)

// Offset 为开平标志。
type Offset int

const (
	Open Offset = iota + 1
	Close
)

// SymLimit 为某合约的账户单边限额、组单边限额与账户日内开仓量上限。
type SymLimit struct {
	Acct    int64
	Group   int64
	DayOpen int64
}

// Registry 为账户登记与合约限额的只读/变更容器（非并发安全，由上层加锁）。
type Registry struct {
	now    int64
	groups map[string]string // 账户 -> 组（登记后固定）
	syms   map[string]SymLimit
}

// NewRegistry 创建空 Registry。
func NewRegistry() *Registry {
	return &Registry{groups: map[string]string{}, syms: map[string]SymLimit{}}
}

// Register 把账户固定归入一个组，重复登记返回 ErrDuplicate。
// 调用方负责参数与时钟校验；时钟仅在校验通过后推进。
func (r *Registry) Register(now int64, acct, group []byte) error {
	if _, ok := r.groups[string(acct)]; ok {
		return ErrDuplicate
	}
	r.groups[string(acct)] = string(group)
	r.now = now
	return nil
}

// SetLimit 设置某合约三项限额，可重复调用（始终接受）。
// 调用方负责参数与时钟校验。
func (r *Registry) SetLimit(now int64, sym []byte, acctLim, groupLim, dayOpen int64) {
	r.syms[string(sym)] = SymLimit{Acct: acctLim, Group: groupLim, DayOpen: dayOpen}
	r.now = now
}

// Group 返回账户所属组及是否已登记。
func (r *Registry) Group(acct []byte) ([]byte, bool) {
	g, ok := r.groups[string(acct)]
	if !ok {
		return nil, false
	}
	return []byte(g), true
}

// Limits 返回合约限额及是否已设置。
func (r *Registry) Limits(sym []byte) (SymLimit, bool) {
	l, ok := r.syms[string(sym)]
	return l, ok
}
