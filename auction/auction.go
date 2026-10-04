// Package auction 实现开盘集合竞价的开盘价确定。
package auction

import "sort"

// Side 为委托方向。
type Side int

const (
	Buy  Side = 1
	Sell Side = 2
)

// Order 是定价所需的在簿委托视图。
type Order struct {
	Seq   int64
	ID    []byte
	Acct  []byte
	Side  Side
	Price int64
	Qty   int64
}

// Stat 是单个候选价的 B/S/V/I 判定依据。
type Stat struct {
	Price     int64
	B         int64
	S         int64
	V         int64
	I         int64
	BuyHeavy  bool // B > S
	SellHeavy bool // B < S
}

// Result 为定价结果。
type Result struct {
	Price   int64 // 0 表示无开盘价
	Stats   []Stat
	visited int64
}

// Visited 返回定价阶段对委托记录的访问次数（排序比较不计）。
func (r Result) Visited() int64 { return r.visited }

// Price 按三级筛选确定开盘价。
func Price(orders []Order, ref int64) Result {
	res := Result{Stats: []Stat{}}
	if len(orders) == 0 {
		return res
	}

	// 候选价：在簿委托出现过的价格，升序去重。
	prices := make([]int64, 0, len(orders))
	seen := make(map[int64]struct{}, len(orders))
	for _, o := range orders {
		if _, ok := seen[o.Price]; !ok {
			seen[o.Price] = struct{}{}
			prices = append(prices, o.Price)
		}
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })

	// 同价位买卖量汇总。
	buyAt := make(map[int64]int64, len(prices))
	sellAt := make(map[int64]int64, len(prices))
	for _, o := range orders {
		res.visited++
		if o.Side == Buy {
			buyAt[o.Price] += o.Qty
		} else {
			sellAt[o.Price] += o.Qty
		}
	}

	// 单次扫描：从最低价起 S 累加卖量；从最高价起 B 累加买量。
	sellCum := make([]int64, len(prices))
	var cum int64
	for i, p := range prices {
		cum += sellAt[p]
		sellCum[i] = cum
	}
	buyCum := make([]int64, len(prices))
	cum = 0
	for i := len(prices) - 1; i >= 0; i-- {
		cum += buyAt[prices[i]]
		buyCum[i] = cum
	}

	type cand struct {
		idx int
		st  Stat
	}
	cands := make([]cand, 0, len(prices))
	for i, p := range prices {
		b, s := buyCum[i], sellCum[i]
		st := Stat{Price: p, B: b, S: s}
		if b >= s {
			st.V, st.I = s, b-s
		} else {
			st.V, st.I = b, s-b
		}
		st.BuyHeavy = b > s
		st.SellHeavy = b < s
		res.Stats = append(res.Stats, st)
		cands = append(cands, cand{i, st})
	}

	// 一级：V 最大。
	best := cands
	filter := func(keep func(cand) bool) {
		out := best[:0]
		for _, c := range best {
			if keep(c) {
				out = append(out, c)
			}
		}
		best = out
	}
	maxV := best[0].st.V
	for _, c := range best {
		if c.st.V > maxV {
			maxV = c.st.V
		}
	}
	filter(func(c cand) bool { return c.st.V == maxV })
	if maxV == 0 {
		return res
	}

	// 二级：I 最小。
	minI := best[0].st.I
	for _, c := range best {
		if c.st.I < minI {
			minI = c.st.I
		}
	}
	filter(func(c cand) bool { return c.st.I == minI })

	// 三级：全买压取最高价；全卖压取最低价；其余取 |p-ref| 最小，并列取低价。
	allBuy, allSell := true, true
	for _, c := range best {
		if !c.st.BuyHeavy {
			allBuy = false
		}
		if !c.st.SellHeavy {
			allSell = false
		}
	}
	pick := best[0]
	switch {
	case allBuy:
		for _, c := range best[1:] {
			if c.st.Price > pick.st.Price {
				pick = c
			}
		}
	case allSell:
		for _, c := range best[1:] {
			if c.st.Price < pick.st.Price {
				pick = c
			}
		}
	default:
		dist := func(p int64) int64 {
			if p >= ref {
				return p - ref
			}
			return ref - p
		}
		bestDist := dist(pick.st.Price)
		for _, c := range best[1:] {
			d := dist(c.st.Price)
			if d < bestDist || (d == bestDist && c.st.Price < pick.st.Price) {
				bestDist, pick = d, c
			}
		}
	}
	res.Price = pick.st.Price
	return res
}
