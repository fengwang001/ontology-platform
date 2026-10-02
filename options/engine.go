// Package options 实现单一系列现金结算期权的到期行权与指派引擎。
//
// 引擎登记多空成对的持仓批次与空头保证金，到期结算时按价内门槛
// 自动行权、按开仓先后（批次序号 seq 升序）指派给空头，再按保证金
// 处理指派方的支付缺口并由行权方按应收比例分担损失。
package options

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

// Kind 为期权类型：认购 Call 或认沽 Put。
type Kind int

const (
	Call Kind = iota
	Put
)

// 可区分的拒绝原因。统一用 errors.Is 判定。
var (
	// ErrInvalidParam 参数非法（构造参数、数量、金额、账户名、结算价越界等）。
	ErrInvalidParam = errors.New("options: invalid parameter")
	// ErrExpired 系列已到期（Settle 成功之后的任何操作）。
	ErrExpired = errors.New("options: series already expired")
	// ErrSelfTrade 自成交（买方等于卖方）。
	ErrSelfTrade = errors.New("options: self trade")
	// ErrNoLong 账户没有多头批次（Abstain 的前提不满足）。
	ErrNoLong = errors.New("options: account has no long position")
)

// 参数边界。
const (
	MaxStrike    = 1_000_000
	MaxMult      = 1_000
	MaxThreshold = 1_000_000
	MaxTradeN    = 1_000_000
	MaxTotalN    = 1_000_000_000
	MaxMarginG   = 1_000_000_000_000
	MaxMarginSum = 1_000_000_000_000_000
	MaxSettleS   = 1_000_000
)

// batch 为一个持仓批次（多头或空头）。
type batch struct {
	acct string
	n    int64
	seq  int64
}

// Exercise 行权清单条目：账户与其行权量。
type Exercise struct {
	Account string
	Qty     int64
}

// Assignment 指派清单条目：空头批次 seq、账户与分得量（含 0）。
type Assignment struct {
	Seq     int64
	Account string
	Qty     int64
}

// Default 违约清单条目：账户与其支付缺口。
type Default struct {
	Account string
	Amount  int64
}

// CashEntry 净现金表条目：账户与其净额（应收 − 分担损失 − 实付）。
type CashEntry struct {
	Account string
	Net     int64
}

// Settlement 为一次成功 Settle 的完整结果。
type Settlement struct {
	// Intrinsic 每单位内在价值 v。
	Intrinsic int64
	// TotalExercised 总行权量 Q。
	TotalExercised int64
	// Exercises 行权清单，按账户字节序升序。
	Exercises []Exercise
	// Assignments 指派清单，按 seq 升序，含全部空头批次。
	Assignments []Assignment
	// Defaults 违约清单，按账户字节序升序，仅含缺口大于 0 的账户。
	Defaults []Default
	// NetCash 净现金表，按账户字节序升序，含全部出现过的账户。
	NetCash []CashEntry
}

// Engine 为单一系列期权的行权与指派引擎，可并发使用。
type Engine struct {
	mu sync.Mutex

	kind   Kind
	k      int64
	mult   int64
	thresh int64

	expired bool
	seqNext int64
	totalN  int64

	longs  []batch
	shorts []batch

	longBatches map[string]int64
	margins     map[string]int64
	abstained   map[string]bool
	accounts    map[string]bool
}

// NewEngine 构造一个系列。类型须为 Call 或 Put，K ∈ [1, 10^6]，
// Mu ∈ [1, 10^3]，T ∈ [1, 10^6]。参数非法返回 ErrInvalidParam。
func NewEngine(kind Kind, k, mult, thresh int64) (*Engine, error) {
	if kind != Call && kind != Put {
		return nil, ErrInvalidParam
	}
	if k < 1 || k > MaxStrike || mult < 1 || mult > MaxMult ||
		thresh < 1 || thresh > MaxThreshold {
		return nil, ErrInvalidParam
	}
	return &Engine{
		kind:        kind,
		k:           k,
		mult:        mult,
		thresh:      thresh,
		seqNext:     1,
		longBatches: make(map[string]int64),
		margins:     make(map[string]int64),
		abstained:   make(map[string]bool),
		accounts:    make(map[string]bool),
	}, nil
}

// Trade 登记一笔成交，新增一个多头批次与一个空头批次，返回批次序号 seq。
//
// 买方与卖方须为不同的非空账户名，n ∈ [1, 10^6]，且全部成功 Trade 的
// n 之和不得超过 10^9。同一账户可同时持有多头与空头批次，两者互不抵消。
// 拒绝时按参数非法、已到期、自成交的顺序只报第一个原因，且不改任何状态。
func (e *Engine) Trade(buyer, seller string, n int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if buyer == "" || seller == "" || n < 1 || n > MaxTradeN ||
		e.totalN+n > MaxTotalN {
		return 0, ErrInvalidParam
	}
	if e.expired {
		return 0, ErrExpired
	}
	if buyer == seller {
		return 0, ErrSelfTrade
	}

	seq := e.seqNext
	e.seqNext++
	e.totalN += n
	e.longs = append(e.longs, batch{acct: buyer, n: n, seq: seq})
	e.shorts = append(e.shorts, batch{acct: seller, n: n, seq: seq})
	e.longBatches[buyer]++
	e.accounts[buyer] = true
	e.accounts[seller] = true
	return seq, nil
}

// Margin 为账户追加保证金。
//
// g ∈ [1, 10^12]，账户保证金累计不得超过 10^15。可对任意非空账户名
// （含从未交易过的账户）调用。拒绝时不改任何状态。
func (e *Engine) Margin(acct string, g int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if acct == "" || g < 1 || g > MaxMarginG ||
		e.margins[acct]+g > MaxMarginSum {
		return ErrInvalidParam
	}
	if e.expired {
		return ErrExpired
	}

	e.margins[acct] += g
	e.accounts[acct] = true
	return nil
}

// Abstain 登记账户放弃自动行权（账户级标志）。
//
// 作用于该账户的全部多头批次（含登记之后、到期之前新增的批次）。
// 要求账户当前至少有一个多头批次；对已放弃的账户重复登记成功且无副作用。
// 拒绝时按参数非法、已到期、无多头持仓的顺序只报第一个原因。
func (e *Engine) Abstain(acct string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if acct == "" {
		return ErrInvalidParam
	}
	if e.expired {
		return ErrExpired
	}
	if e.longBatches[acct] == 0 {
		return ErrNoLong
	}

	e.abstained[acct] = true
	e.accounts[acct] = true
	return nil
}

// Settle 以结算价 S 到期结算，只能成功一次。
//
// S ∈ [1, 10^6]。因参数非法被拒时不使系列到期；成功后的任何操作
// （含再次 Settle）都报 ErrExpired。并发多次 Settle 中恰有一次成功。
func (e *Engine) Settle(s int64) (*Settlement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if s < 1 || s > MaxSettleS {
		return nil, ErrInvalidParam
	}
	if e.expired {
		return nil, ErrExpired
	}
	e.expired = true

	v := e.intrinsic(s)
	st := &Settlement{Intrinsic: v}

	// 行权：v >= T 时，每个未放弃账户的全部多头批次自动行权。
	exerciseQty := make(map[string]int64)
	if v >= e.thresh {
		for _, b := range e.longs {
			if !e.abstained[b.acct] {
				exerciseQty[b.acct] += b.n
			}
		}
	}
	exercising := sortedKeys(exerciseQty)
	for _, a := range exercising {
		st.Exercises = append(st.Exercises, Exercise{Account: a, Qty: exerciseQty[a]})
		st.TotalExercised += exerciseQty[a]
	}

	// 指派：空头批次按 seq 升序（登记顺序即升序）依次分配 Q。
	owe := make(map[string]int64)
	remaining := st.TotalExercised
	for _, b := range e.shorts {
		q := min(b.n, remaining)
		remaining -= q
		st.Assignments = append(st.Assignments, Assignment{Seq: b.seq, Account: b.acct, Qty: q})
		if q > 0 {
			owe[b.acct] += v * q * e.mult
		}
	}

	// 现金：实付、缺口与损失分摊。
	recv := make(map[string]int64, len(exercising))
	var totalRecv int64
	for _, a := range exercising {
		recv[a] = v * exerciseQty[a] * e.mult
		totalRecv += recv[a]
	}

	pay := make(map[string]int64)
	var totalGap int64
	for _, a := range sortedKeys(owe) {
		pay[a] = min(e.margins[a], owe[a])
		if d := owe[a] - pay[a]; d > 0 {
			st.Defaults = append(st.Defaults, Default{Account: a, Amount: d})
			totalGap += d
		}
	}

	loss := e.shareLoss(totalGap, recv, totalRecv, exercising)

	for _, a := range sortedKeys(e.accounts) {
		st.NetCash = append(st.NetCash, CashEntry{
			Account: a,
			Net:     recv[a] - loss[a] - pay[a],
		})
	}
	return st, nil
}

// intrinsic 计算结算价 s 下每单位内在价值。
func (e *Engine) intrinsic(s int64) int64 {
	if e.kind == Call {
		return max(s-e.k, 0)
	}
	return max(e.k-s, 0)
}

// shareLoss 把总缺口 delta 按应收比例分摊给行权账户。
//
// loss_a = floor(delta * recv_a / totalRecv)，乘积用 big.Int 计算以免
// 溢出 64 位；余量按账户字节序升序每个行权账户各加 1 直到分完。
func (e *Engine) shareLoss(delta int64, recv map[string]int64, totalRecv int64, exercising []string) map[string]int64 {
	loss := make(map[string]int64)
	if delta <= 0 {
		return loss
	}
	biDelta := big.NewInt(delta)
	biTotal := big.NewInt(totalRecv)
	var distributed int64
	for _, a := range exercising {
		prod := new(big.Int).Mul(biDelta, big.NewInt(recv[a]))
		l := new(big.Int).Quo(prod, biTotal).Int64()
		loss[a] = l
		distributed += l
	}
	for rem := delta - distributed; rem > 0; rem-- {
		// 余量小于行权账户数，一轮即可分完。
		for _, a := range exercising {
			if rem == 0 {
				break
			}
			loss[a]++
			rem--
		}
	}
	return loss
}

// sortedKeys 返回键按字节序升序的切片。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Expired 报告系列是否已到期（Settle 已成功）。
func (e *Engine) Expired() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.expired
}

// MarginOf 返回账户的累计保证金。
func (e *Engine) MarginOf(acct string) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.margins[acct]
}

// Abstained 报告账户是否已登记放弃自动行权。
func (e *Engine) Abstained(acct string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.abstained[acct]
}

// LongQty 返回账户多头批次的合约总量。
func (e *Engine) LongQty(acct string) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var q int64
	for _, b := range e.longs {
		if b.acct == acct {
			q += b.n
		}
	}
	return q
}

// ShortQty 返回账户空头批次的合约总量。
func (e *Engine) ShortQty(acct string) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var q int64
	for _, b := range e.shorts {
		if b.acct == acct {
			q += b.n
		}
	}
	return q
}
