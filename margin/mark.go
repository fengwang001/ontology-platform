package margin

import (
	"cmp"
	"math/big"
	"slices"
)

// equity 计算权益 E = M + q*P - C。
func (a *Account) equity(p int64) int64 {
	return a.M + a.Q*p - a.C
}

// pnl 计算浮动盈亏 q*P - C。
func (a *Account) pnl(p int64) int64 {
	return a.Q*p - a.C
}

// Mark 将标记价格推进到 P，并在一个原子步骤内完成三步：
//  1. 按 Mark 开始时的状态一次性定出强平集合 Λ（有仓位且
//     E = M + q*P - C 严格小于 R = ceil(|q|*P*Mm/10000)）；
//  2. 按账户编号字节序升序逐个执行 Λ 中的强平，保险基金逐步
//     演化，亏空先由 Z 吸收，不足部分按盈利率排序自动减仓对手方，
//     候选用尽后的剩余记入 B；
//  3. 返回按执行次序的强平清单。
//
// 仅当 P 越界时返回 ErrInvalidParam，且不改变任何状态。
func (e *Engine) Mark(p int64) ([]Liquidation, error) {
	if p < 1 || p > maxPrice {
		return nil, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// 第一步：一次性定出强平集合 Λ，此后不再改变。
	lambda := make(map[string]bool)
	for id, acct := range e.accounts {
		if acct.Q == 0 {
			continue
		}
		r := ceilDiv(abs64(acct.Q)*p*e.mm, rateBase)
		if acct.equity(p) < r {
			lambda[id] = true
		}
	}

	// 第二步：按账户编号字节序升序逐个执行。
	order := make([]string, 0, len(lambda))
	for id := range lambda {
		order = append(order, id)
	}
	slices.Sort(order)

	var result []Liquidation
	for _, id := range order {
		result = append(result, e.liquidate(id, p, lambda))
	}

	// 第三步：记录标记价格并返回清单。
	e.mark = p
	e.hasMark = true
	return result, nil
}

// liquidate 以价格 P 对账户 id 全部平仓并结算罚金、亏空与自动减仓。
// 调用方须持有锁。返回该账户的执行记录。
func (e *Engine) liquidate(id string, p int64, lambda map[string]bool) Liquidation {
	acct := e.accounts[id]
	rec := Liquidation{Account: id, Equity: acct.equity(p)}
	q := acct.Q

	if rec.Equity >= 0 {
		// 罚金 f = min(E, floor(|q|*P*F/10000))，不产生亏空。
		rec.Fee = min(rec.Equity, abs64(q)*p*e.f/rateBase)
		acct.M = rec.Equity - rec.Fee
		e.z += rec.Fee
	} else {
		// 亏空 d = -E，先由 Z 吸收，余额触发自动减仓。
		acct.M = 0
		d := -rec.Equity
		rec.FundUsed = min(e.z, d)
		e.z -= rec.FundUsed
		if s := d - rec.FundUsed; s > 0 {
			rec.ADL, rec.BadDebt = e.autoDeleverage(id, q, p, s, lambda)
			e.b += rec.BadDebt
		}
	}

	acct.Q = 0
	acct.C = 0
	return rec
}

// autoDeleverage 为亏空余额 s 执行自动减仓：候选为当前有仓位、
// 方向与被强平账户相反、不属于 Λ 且浮盈 pnl = q*P - C 严格大于 0
// 的账户，按盈利率 pnl/|C| 从大到小排序（大整数交叉相乘比较，
// 相等取账户编号字节序小者在前），依次取走
// take = min(s 剩余, 该候选当前 pnl)，方式为令候选的 C 增加 take。
// 候选用尽后的剩余作为坏账返回。
func (e *Engine) autoDeleverage(liquidated string, lq, p, s int64, lambda map[string]bool) ([]ADLEntry, int64) {
	type candidate struct {
		id  string
		pnl int64
		c   int64 // |C|
	}
	var cands []candidate
	for id, acct := range e.accounts {
		if id == liquidated || lambda[id] || acct.Q == 0 {
			continue
		}
		if (acct.Q > 0) == (lq > 0) {
			continue // 方向相同，跳过
		}
		if pnl := acct.pnl(p); pnl > 0 {
			cands = append(cands, candidate{id: id, pnl: pnl, c: abs64(acct.C)})
		}
	}
	slices.SortFunc(cands, func(x, y candidate) int {
		// 比较 x.pnl/x.c 与 y.pnl/y.c，乘积可达 10^24，用大整数。
		lhs := new(big.Int).Mul(big.NewInt(x.pnl), big.NewInt(y.c))
		rhs := new(big.Int).Mul(big.NewInt(y.pnl), big.NewInt(x.c))
		if c := lhs.Cmp(rhs); c != 0 {
			return -c // 盈利率大者在前
		}
		return cmp.Compare(x.id, y.id) // 相等取编号字节序小者
	})

	var entries []ADLEntry
	for _, cand := range cands {
		if s == 0 {
			break
		}
		acct := e.accounts[cand.id]
		take := min(s, acct.pnl(p)) // 当前 pnl，排序后 C 未被本次减仓改写时等于 cand.pnl
		if take <= 0 {
			continue
		}
		acct.C += take // 权益恰好减少 take，M 与 q 不变
		entries = append(entries, ADLEntry{Account: cand.id, Take: take})
		s -= take
	}
	return entries, s
}
