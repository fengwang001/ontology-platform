// Package interest 实现活期存款的积数计息器。
//
// 计息规则：年利率 r 以万分比整数记，按 360 天计，令 D = 3600000。
// 结息区间左闭右开 [prev, d)，对区间内每一天按「日终余额 × 当日年利率」
// 累计积数，加上上次结息余数 R 得 N，利息 i = floor(N / D)，
// 新余数 R = N mod D（恒满足 0 <= R < D）。
package interest

import (
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// D 为积数到利息的换算除数：360 天 × 万分比 10000。
const D int64 = 3600000

const (
	maxStart          int64 = 1_000_000
	maxDate           int64 = 10_000_000
	maxAmount         int64 = 1_000_000_000
	maxRate           int64 = 10_000
	maxDepositBalance int64 = 1_000_000_000
	maxBalance        int64 = 100_000_000_000
	maxSpan           int64 = 3660
)

// ErrKind 区分操作被拒绝的原因。
type ErrKind int

const (
	// ErrInvalidArg 参数非法（越界、delta 为 0、结息覆盖天数超过 3660 等）。
	ErrInvalidArg ErrKind = iota + 1
	// ErrOutOfOrder 时序倒退：d 小于已接受操作的最大日期 m。
	ErrOutOfOrder
	// ErrNotSettled 区间未结息：Correct 的 d 不小于 prev。
	ErrNotSettled
	// ErrEmptyInterval 空区间：Settle 的 d 不大于 prev。
	ErrEmptyInterval
	// ErrInsufficient 余额不足：Withdraw 的 x 大于当前余额。
	ErrInsufficient
	// ErrBalanceOverflow 余额越限：Deposit 后余额超过 1e9。
	ErrBalanceOverflow
	// ErrCorrectNegative 冲正后余额为负。
	ErrCorrectNegative
	// ErrCorrectOverflow 冲正后余额越限（超过 1e11）。
	ErrCorrectOverflow
)

// Error 为操作被拒绝时返回的错误，Kind 可区分具体原因。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(kind ErrKind, format string, args ...interface{}) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Record 为一条结息记录：覆盖区间 [Start, Date)，贷记利息 I，结息后余数 R。
type Record struct {
	Start int64
	Date  int64
	I     int64
	R     int64
}

// Change 描述一次冲正引起某条结息记录的变化。
type Change struct {
	Date       int64
	OldI, NewI int64
	OldR, NewR int64
}

// Account 为活期存款计息账户，所有方法可并发调用。
type Account struct {
	mu sync.Mutex

	start   int64 // 起始日
	m       int64 // 已接受的前四类操作的最大日期（无操作时为 start）
	prev    int64 // 上次结息日
	rem     int64 // 余数 R，恒满足 0 <= R < D
	balance int64 // 当前余额

	flowTotal int64   // 全部存取与冲正的净额之和
	flowDates []int64 // 有序：发生存取/冲正的日期
	flowVals  []int64 // 与 flowDates 对应的当日净额
	flowPre   []int64 // flowVals 的前缀和

	rateDates []int64 // 有序：SetRate 的日期
	rateVals  []int64 // 与 rateDates 对应的年利率（同日取最后一次）

	recs   []Record // 结息记录，按结息日严格递增
	intPre []int64  // 各记录利息 I 的前缀和
}

// New 构造起始日为 start 的账户：初始余额 0、年利率 0、prev = start、R = 0。
func New(start int64) (*Account, error) {
	if start < 0 || start > maxStart {
		return nil, newError(ErrInvalidArg, "start %d 越界，须在 [0, %d] 内", start, maxStart)
	}
	return &Account{start: start, m: start, prev: start}, nil
}

// Balance 返回当前余额。
func (a *Account) Balance() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.balance
}

// Remainder 返回当前余数 R。
func (a *Account) Remainder() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rem
}

// Prev 返回上次结息日。
func (a *Account) Prev() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prev
}

// Records 返回当前全部结息记录的副本。
func (a *Account) Records() []Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Record, len(a.recs))
	copy(out, a.recs)
	return out
}

// prefixAt 返回有序日期 dates 中不超过 k 的最后一个下标对应的前缀和。
func prefixAt(dates, pre []int64, k int64) int64 {
	i := sort.Search(len(dates), func(i int) bool { return dates[i] > k }) - 1
	if i < 0 {
		return 0
	}
	return pre[i]
}

// rebuildFlowPre 重建存取净额前缀和。
func (a *Account) rebuildFlowPre() {
	a.flowPre = make([]int64, len(a.flowVals))
	var sum int64
	for i, v := range a.flowVals {
		sum += v
		a.flowPre[i] = sum
	}
}

// rebuildIntPre 重建结息利息前缀和。
func (a *Account) rebuildIntPre() {
	a.intPre = make([]int64, len(a.recs))
	var sum int64
	for i, r := range a.recs {
		sum += r.I
		a.intPre[i] = sum
	}
}

// recDates 返回各结息记录的结息日（与 a.recs 同序，严格递增）。
func (a *Account) recDates() []int64 {
	dates := make([]int64, len(a.recs))
	for i, r := range a.recs {
		dates[i] = r.Date
	}
	return dates
}

// balanceAt 返回第 k 天的日终余额：不超过 k 的存取冲正净额之和，
// 加上结息日不超过 k 的各次结息贷记利息之和。
func (a *Account) balanceAt(k int64) int64 {
	return prefixAt(a.flowDates, a.flowPre, k) + prefixAt(a.recDates(), a.intPre, k)
}

// rateAt 返回第 k 天的年利率：日期不超过 k 的最后一次 SetRate 的值，没有则为 0。
func (a *Account) rateAt(k int64) int64 {
	i := sort.Search(len(a.rateDates), func(i int) bool { return a.rateDates[i] > k }) - 1
	if i < 0 {
		return 0
	}
	return a.rateVals[i]
}

// addFlow 在第 d 天登记净额 delta（可正可负）。
func (a *Account) addFlow(d, delta int64) {
	i := sort.Search(len(a.flowDates), func(i int) bool { return a.flowDates[i] >= d })
	if i < len(a.flowDates) && a.flowDates[i] == d {
		a.flowVals[i] += delta
	} else {
		a.flowDates = append(a.flowDates, 0)
		copy(a.flowDates[i+1:], a.flowDates[i:])
		a.flowDates[i] = d
		a.flowVals = append(a.flowVals, 0)
		copy(a.flowVals[i+1:], a.flowVals[i:])
		a.flowVals[i] = delta
	}
	a.flowTotal += delta
	a.rebuildFlowPre()
}

// nextEvent 返回三个事件日期序列中严格大于 k 的最小事件日，没有则返回 hi+1。
func (a *Account) nextEvent(k, hi int64, recDates []int64) int64 {
	next := hi + 1
	for _, dates := range [][]int64{a.flowDates, a.rateDates, recDates} {
		i := sort.Search(len(dates), func(i int) bool { return dates[i] > k })
		if i < len(dates) && dates[i] < next {
			next = dates[i]
		}
	}
	return next
}

// accumulate 累计区间 [lo, hi]（闭区间）上每日「日终余额 × 当日年利率」的积数之和。
// 余额与利率在相邻事件日之间保持不变，按段累加以跳过无变化的区间。
func (a *Account) accumulate(lo, hi int64) *big.Int {
	total := new(big.Int)
	recDates := a.recDates()
	for k := lo; k <= hi; {
		b := a.balanceAt(k)
		r := a.rateAt(k)
		end := a.nextEvent(k, hi, recDates) - 1
		if b != 0 && r != 0 {
			t := big.NewInt(b)
			t.Mul(t, big.NewInt(r))
			t.Mul(t, big.NewInt(end-k+1))
			total.Add(total, t)
		}
		k = end + 1
	}
	return total
}

var bigD = big.NewInt(D)

// divmod 返回 floor(n / D) 与 n mod D，余数恒满足 0 <= r < D。
func divmod(n *big.Int) (q, r int64) {
	Q := new(big.Int)
	M := new(big.Int)
	Q.DivMod(n, bigD, M)
	return Q.Int64(), M.Int64()
}

// checkDate 校验操作日期 d 的基本范围。
func checkDate(d int64) *Error {
	if d < 0 || d > maxDate {
		return newError(ErrInvalidArg, "日期 %d 越界，须在 [0, %d] 内", d, maxDate)
	}
	return nil
}

// Deposit 在第 d 天存入 x（1 <= x <= 1e9），存入后余额不得超过 1e9。
func (a *Account) Deposit(d, x int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := checkDate(d); e != nil {
		return e
	}
	if x < 1 || x > maxAmount {
		return newError(ErrInvalidArg, "存入金额 %d 越界，须在 [1, %d] 内", x, maxAmount)
	}
	if d < a.m {
		return newError(ErrOutOfOrder, "日期 %d 小于已接受操作的最大日期 %d", d, a.m)
	}
	if a.balance+x > maxDepositBalance {
		return newError(ErrBalanceOverflow, "存入后余额 %d 超过上限 %d", a.balance+x, maxDepositBalance)
	}
	a.addFlow(d, x)
	a.balance += x
	a.m = d
	return nil
}

// Withdraw 在第 d 天取出 x（1 <= x <= 1e9），x 不得大于当前余额。
func (a *Account) Withdraw(d, x int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := checkDate(d); e != nil {
		return e
	}
	if x < 1 || x > maxAmount {
		return newError(ErrInvalidArg, "取出金额 %d 越界，须在 [1, %d] 内", x, maxAmount)
	}
	if d < a.m {
		return newError(ErrOutOfOrder, "日期 %d 小于已接受操作的最大日期 %d", d, a.m)
	}
	if x > a.balance {
		return newError(ErrInsufficient, "取出 %d 超过当前余额 %d", x, a.balance)
	}
	a.addFlow(d, -x)
	a.balance -= x
	a.m = d
	return nil
}

// SetRate 在第 d 天把年利率调整为 r（0 <= r <= 10000，万分比），当日起生效。
func (a *Account) SetRate(d, r int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := checkDate(d); e != nil {
		return e
	}
	if r < 0 || r > maxRate {
		return newError(ErrInvalidArg, "年利率 %d 越界，须在 [0, %d] 内", r, maxRate)
	}
	if d < a.m {
		return newError(ErrOutOfOrder, "日期 %d 小于已接受操作的最大日期 %d", d, a.m)
	}
	if n := len(a.rateDates); n > 0 && a.rateDates[n-1] == d {
		a.rateVals[n-1] = r
	} else {
		a.rateDates = append(a.rateDates, d)
		a.rateVals = append(a.rateVals, r)
	}
	a.m = d
	return nil
}

// Settle 在结息日 d 结息：覆盖区间 [prev, d)，返回利息、新余额与新余数。
// 覆盖天数不得超过 3660；贷记利息自第 d 天的日终余额起计入。
func (a *Account) Settle(d int64) (i, balance, rem int64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := checkDate(d); e != nil {
		return 0, 0, 0, e
	}
	if d-a.prev > maxSpan {
		return 0, 0, 0, newError(ErrInvalidArg, "结息覆盖天数 %d 超过上限 %d", d-a.prev, maxSpan)
	}
	if d < a.m {
		return 0, 0, 0, newError(ErrOutOfOrder, "日期 %d 小于已接受操作的最大日期 %d", d, a.m)
	}
	if d <= a.prev {
		return 0, 0, 0, newError(ErrEmptyInterval, "结息日 %d 不大于上次结息日 %d，区间为空", d, a.prev)
	}
	N := a.accumulate(a.prev, d-1)
	N.Add(N, big.NewInt(a.rem))
	i, rem = divmod(N)
	a.recs = append(a.recs, Record{Start: a.prev, Date: d, I: i, R: rem})
	a.rebuildIntPre()
	a.rem = rem
	a.prev = d
	a.balance += i
	a.m = d
	return i, a.balance, a.rem, nil
}

// Correct 冲正第 d 天（start <= d < prev）的存取 delta（非零，|delta| <= 1e9）：
// 第 d 天起每日日终余额增加 delta，随后对结息日严格大于 d 的结息记录
// 按先后次序逐条重算利息与余数。重算与检查为原子步骤：
// 若第 d 天到最大已接受操作日期 m 间任一日日终余额为负或超过 1e11，
// 整个冲正不生效。返回新余额、新余数与发生变化的结息记录列表。
func (a *Account) Correct(d, delta int64) (balance, rem int64, changes []Change, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := checkDate(d); e != nil {
		return 0, 0, nil, e
	}
	if d < a.start {
		return 0, 0, nil, newError(ErrInvalidArg, "冲正日期 %d 小于起始日 %d", d, a.start)
	}
	if delta == 0 || delta > maxAmount || delta < -maxAmount {
		return 0, 0, nil, newError(ErrInvalidArg, "冲正额 %d 越界，须为非零且绝对值不超过 %d", delta, maxAmount)
	}
	if d >= a.prev {
		return 0, 0, nil, newError(ErrNotSettled, "冲正日期 %d 不小于上次结息日 %d，区间未结息", d, a.prev)
	}

	// 快照，校验失败时整体回滚。
	savedFlowDates := append([]int64(nil), a.flowDates...)
	savedFlowVals := append([]int64(nil), a.flowVals...)
	savedFlowTotal := a.flowTotal
	savedRecs := append([]Record(nil), a.recs...)
	savedBalance, savedRem := a.balance, a.rem
	rollback := func() {
		a.flowDates, a.flowVals = savedFlowDates, savedFlowVals
		a.flowTotal = savedFlowTotal
		a.recs = savedRecs
		a.balance, a.rem = savedBalance, savedRem
		a.rebuildFlowPre()
		a.rebuildIntPre()
	}

	a.addFlow(d, delta)

	// 结息日严格大于 d 的记录构成有序后缀，逐条重算。
	recDates := a.recDates()
	idx := sort.Search(len(a.recs), func(j int) bool { return recDates[j] > d })
	Rin := int64(0)
	if idx > 0 {
		Rin = a.recs[idx-1].R // 前一条记录不受影响，其余数保持原值
	}
	for j := idx; j < len(a.recs); j++ {
		a.rebuildIntPre() // 前序记录已更新，重建利息前缀和
		rec := &a.recs[j]
		N := a.accumulate(rec.Start, rec.Date-1)
		N.Add(N, big.NewInt(Rin))
		newI, newR := divmod(N)
		if newI != rec.I || newR != rec.R {
			changes = append(changes, Change{
				Date: rec.Date,
				OldI: rec.I, NewI: newI,
				OldR: rec.R, NewR: newR,
			})
			rec.I, rec.R = newI, newR
		}
		Rin = newR
	}
	a.rebuildIntPre()

	// 检查第 d 天到 m 的每日日终余额：余额在相邻事件日之间不变，
	// 只需检查 [d, m] 内每个事件日处的取值（d 本身必为存取事件日）。
	neg, over := false, false
	recDates = a.recDates()
	fi := sort.Search(len(a.flowDates), func(i int) bool { return a.flowDates[i] >= d })
	ri := sort.Search(len(recDates), func(i int) bool { return recDates[i] >= d })
	for {
		var e int64
		ok := false
		if fi < len(a.flowDates) && a.flowDates[fi] <= a.m {
			e, ok = a.flowDates[fi], true
		}
		if ri < len(recDates) && recDates[ri] <= a.m && (!ok || recDates[ri] < e) {
			e, ok = recDates[ri], true
		}
		if !ok {
			break
		}
		b := a.balanceAt(e)
		if b < 0 {
			neg = true
		}
		if b > maxBalance {
			over = true
		}
		for fi < len(a.flowDates) && a.flowDates[fi] <= e {
			fi++
		}
		for ri < len(recDates) && recDates[ri] <= e {
			ri++
		}
	}
	if neg {
		rollback()
		return 0, 0, nil, newError(ErrCorrectNegative, "冲正后存在日终余额为负的日期")
	}
	if over {
		rollback()
		return 0, 0, nil, newError(ErrCorrectOverflow, "冲正后存在日终余额超过 %d 的日期", maxBalance)
	}

	a.rem = a.recs[len(a.recs)-1].R
	a.balance = a.flowTotal + a.intPre[len(a.intPre)-1]
	return a.balance, a.rem, changes, nil
}
