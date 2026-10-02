// Package freeze 实现银行账户的司法冻结与轮候冻结管理器。
//
// 账户状态为余额 B 与按登记先后排列的冻结令队列。时刻 t 的有效状态定义为：
// 先把队列中 0 < exp <= t 的令全部移出（exp 恰等于 t 即已失效），再按下式
// 从队首起逐令计算第 i 道令的有效冻结额：
//
//	e_i = min(a_i, max(0, B - 前面各令有效冻结额之和))
//
// 可用余额 V = B - 全部 e_i 之和。有效状态只是 B 与队列的纯函数，不单独存储。
package freeze

import (
	"fmt"
	"sync"
)

// 数值范围常量。
const (
	MaxAmount  = int64(1_000_000_000_000)     // x、a、t、exp 的上界 10^12
	MaxBalance = int64(1_000_000_000_000_000) // 存入后 B 的上界 10^15
)

// ErrCode 区分被拒绝操作的原因，按判定优先级递增。
type ErrCode int

const (
	ErrInvalidParam      ErrCode = iota // 参数非法（空编号、金额/exp/t 越界、存入后 B 超上限）
	ErrTimeRegression                   // 时序倒退：t 小于已接受操作与查询的最大时刻 m
	ErrAccountNotFound                  // 账户不存在（Deposit 除外）
	ErrDuplicateOrder                   // 令编号重复（Freeze，在失效清理之后判定）
	ErrOrderNotFound                    // 令不存在（Unfreeze 与 Seize，已失效的令也算不存在）
	ErrUnfreezeExceeds                  // 解冻超额：x 大于 a_i
	ErrSeizeExceeds                     // 扣划超额：Seize 的 x 大于 e_i
	ErrSeizeQExceeds                    // 扣划超额：SeizeQ 的 x 大于全部 e_i 之和
	ErrInsufficientAvail                // 可用不足：Debit 的 x 大于 V
)

// Error 是操作被拒绝时返回的错误，Code 可区分拒绝原因。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Order 是一道冻结令。A 为当前名义金额，Exp 为到期时刻
// （0 表示永不到期，否则为大于登记时刻且不超过 10^12 的整数）。
type Order struct {
	ID  string
	A   int64
	Exp int64
}

// OrderView 是 Query 返回的单道令快照，含有效冻结额 E。
type OrderView struct {
	ID  string
	A   int64
	E   int64
	Exp int64
}

// AccountView 是 Query 返回的账户有效状态快照。
type AccountView struct {
	B      int64       // 余额
	V      int64       // 可用余额
	Orders []OrderView // 按队列顺序排列的各令
}

type account struct {
	b     int64
	queue []Order
}

// purgeExpired 返回移除了 0 < exp <= t 的令之后的队列（纯函数，不改原切片）。
func purgeExpired(queue []Order, t int64) []Order {
	first := -1
	for i := range queue {
		if o := queue[i]; o.Exp > 0 && o.Exp <= t {
			first = i
			break
		}
	}
	if first < 0 {
		return queue
	}
	out := make([]Order, 0, len(queue))
	out = append(out, queue[:first]...)
	for _, o := range queue[first+1:] {
		if o.Exp == 0 || o.Exp > t {
			out = append(out, o)
		}
	}
	return out
}

// effectiveAmounts 从队首起逐令计算 e_i = min(a_i, max(0, B - 前面各令 e 之和))。
func effectiveAmounts(b int64, queue []Order) []int64 {
	es := make([]int64, len(queue))
	used := int64(0)
	for i := range queue {
		rem := b - used
		if rem < 0 {
			rem = 0
		}
		e := queue[i].A
		if e > rem {
			e = rem
		}
		es[i] = e
		used += e
	}
	return es
}

func validTime(t int64) bool   { return t >= 0 && t <= MaxAmount }
func validAmount(x int64) bool { return x >= 1 && x <= MaxAmount }

func validExp(exp, t int64) bool {
	return exp == 0 || (exp > t && exp <= MaxAmount)
}

// Manager 管理全部账户。所有方法可并发调用，
// 结果等价于某个串行顺序，SeizeQ 是一个原子步骤。
type Manager struct {
	mu       sync.Mutex
	accounts map[string]*account
	m        int64 // 全部账户已接受操作与查询的最大时刻
}

// NewManager 创建空的管理器。
func NewManager() *Manager {
	return &Manager{accounts: make(map[string]*account)}
}

// MaxTime 返回全部账户已接受操作与查询的最大时刻 m。
func (mg *Manager) MaxTime() int64 {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	return mg.m
}

// checkCommon 校验账户编号与时刻的合法性及时序，返回查询类操作的账户。
func (mg *Manager) checkCommon(acct string, t int64) (*account, error) {
	if acct == "" || !validTime(t) {
		return nil, newError(ErrInvalidParam, "invalid param: acct=%q t=%d", acct, t)
	}
	if t < mg.m {
		return nil, newError(ErrTimeRegression, "time regression: t=%d < m=%d", t, mg.m)
	}
	acc, ok := mg.accounts[acct]
	if !ok {
		return nil, newError(ErrAccountNotFound, "account not found: %q", acct)
	}
	return acc, nil
}

// Deposit 对不存在的账户先创建再令 B 增加 x。
func (mg *Manager) Deposit(acct string, t, x int64) error {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	acc, exists := mg.accounts[acct]
	b := int64(0)
	if exists {
		b = acc.b
	}
	if acct == "" || !validTime(t) || !validAmount(x) || b+x > MaxBalance {
		return newError(ErrInvalidParam, "invalid param: acct=%q t=%d x=%d", acct, t, x)
	}
	if t < mg.m {
		return newError(ErrTimeRegression, "time regression: t=%d < m=%d", t, mg.m)
	}
	if !exists {
		acc = &account{}
		mg.accounts[acct] = acc
	}
	acc.queue = purgeExpired(acc.queue, t)
	acc.b += x
	mg.m = t
	return nil
}

// Debit 要求 x 不大于 V，令 B 减少 x。
func (mg *Manager) Debit(acct string, t, x int64) error {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	if !validAmount(x) {
		return newError(ErrInvalidParam, "invalid param: x=%d", x)
	}
	acc, err := mg.checkCommon(acct, t)
	if err != nil {
		return err
	}
	queue := purgeExpired(acc.queue, t)
	es := effectiveAmounts(acc.b, queue)
	used := int64(0)
	for _, e := range es {
		used += e
	}
	if v := acc.b - used; x > v {
		return newError(ErrInsufficientAvail, "insufficient available: x=%d > V=%d", x, v)
	}
	acc.queue = queue
	acc.b -= x
	mg.m = t
	return nil
}

// Freeze 把新令追加到队尾。a 可以大于当前余额，超出部分即轮候。
func (mg *Manager) Freeze(acct string, t int64, id string, a, exp int64) error {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	if id == "" || !validAmount(a) || !validExp(exp, t) {
		return newError(ErrInvalidParam, "invalid param: id=%q a=%d exp=%d t=%d", id, a, exp, t)
	}
	acc, err := mg.checkCommon(acct, t)
	if err != nil {
		return err
	}
	queue := purgeExpired(acc.queue, t)
	for _, o := range queue {
		if o.ID == id {
			return newError(ErrDuplicateOrder, "duplicate order id: %q", id)
		}
	}
	acc.queue = append(queue, Order{ID: id, A: a, Exp: exp})
	mg.m = t
	return nil
}

// Unfreeze 要求 1 <= x <= a_i，令 a_i 减少 x，减为 0 则整令移出队列。
func (mg *Manager) Unfreeze(acct string, t int64, id string, x int64) error {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	if id == "" || !validAmount(x) {
		return newError(ErrInvalidParam, "invalid param: id=%q x=%d", id, x)
	}
	acc, err := mg.checkCommon(acct, t)
	if err != nil {
		return err
	}
	queue := purgeExpired(acc.queue, t)
	idx := indexOf(queue, id)
	if idx < 0 {
		return newError(ErrOrderNotFound, "order not found: %q", id)
	}
	if x > queue[idx].A {
		return newError(ErrUnfreezeExceeds, "unfreeze exceeds: x=%d > a=%d", x, queue[idx].A)
	}
	acc.queue = reduceOrder(queue, idx, x)
	mg.m = t
	return nil
}

// Seize 为对单令的司法划扣，要求 1 <= x <= e_i，
// 令 B 与 a_i 各减少 x，a_i 减为 0 则整令移出。
func (mg *Manager) Seize(acct string, t int64, id string, x int64) error {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	if id == "" || !validAmount(x) {
		return newError(ErrInvalidParam, "invalid param: id=%q x=%d", id, x)
	}
	acc, err := mg.checkCommon(acct, t)
	if err != nil {
		return err
	}
	queue := purgeExpired(acc.queue, t)
	idx := indexOf(queue, id)
	if idx < 0 {
		return newError(ErrOrderNotFound, "order not found: %q", id)
	}
	e := effectiveAmounts(acc.b, queue)[idx]
	if x > e {
		return newError(ErrSeizeExceeds, "seize exceeds: x=%d > e=%d", x, e)
	}
	acc.queue = reduceOrder(queue, idx, x)
	acc.b -= x
	mg.m = t
	return nil
}

// SeizeQ 为按队列顺序的总额划扣，要求 1 <= x <= 全部 e_i 之和
// （先判后改，不得部分执行），按开始时的 e_i 快照从队首起逐令
// 扣 min(e_i, 剩余待扣量），a_i 减为 0 的整令移出。
func (mg *Manager) SeizeQ(acct string, t, x int64) error {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	if !validAmount(x) {
		return newError(ErrInvalidParam, "invalid param: x=%d", x)
	}
	acc, err := mg.checkCommon(acct, t)
	if err != nil {
		return err
	}
	queue := purgeExpired(acc.queue, t)
	es := effectiveAmounts(acc.b, queue)
	total := int64(0)
	for _, e := range es {
		total += e
	}
	if x > total {
		return newError(ErrSeizeQExceeds, "seizeQ exceeds: x=%d > total e=%d", x, total)
	}
	rem := x
	out := queue[:0]
	for i, o := range queue {
		d := es[i]
		if d > rem {
			d = rem
		}
		rem -= d
		o.A -= d
		if o.A > 0 {
			out = append(out, o)
		}
	}
	acc.queue = out
	acc.b -= x
	mg.m = t
	return nil
}

// Query 返回有效状态下的 B、V 与各令（编号、a、e、exp）。
func (mg *Manager) Query(acct string, t int64) (AccountView, error) {
	mg.mu.Lock()
	defer mg.mu.Unlock()

	acc, err := mg.checkCommon(acct, t)
	if err != nil {
		return AccountView{}, err
	}
	queue := purgeExpired(acc.queue, t)
	es := effectiveAmounts(acc.b, queue)
	view := AccountView{B: acc.b, Orders: make([]OrderView, len(queue))}
	used := int64(0)
	for i, o := range queue {
		view.Orders[i] = OrderView{ID: o.ID, A: o.A, E: es[i], Exp: o.Exp}
		used += es[i]
	}
	view.V = acc.b - used
	acc.queue = queue
	mg.m = t
	return view, nil
}

func indexOf(queue []Order, id string) int {
	for i := range queue {
		if queue[i].ID == id {
			return i
		}
	}
	return -1
}

// reduceOrder 将第 idx 道令的 a 减少 x，减为 0 则整令移出队列。
func reduceOrder(queue []Order, idx int, x int64) []Order {
	queue[idx].A -= x
	if queue[idx].A > 0 {
		return queue
	}
	out := make([]Order, 0, len(queue)-1)
	out = append(out, queue[:idx]...)
	return append(out, queue[idx+1:]...)
}
