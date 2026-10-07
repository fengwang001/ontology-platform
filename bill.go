package billing

import (
	"math/big"
	"sort"
)

// 账期是左闭右开区间。结算强制账期首尾相接（第一个账期起点自由，此后每个
// 账期 Start 必须等于上一账期 End），因此读数跨度可在每个边界独立切分，
// 结果与结算次序无关，单账期重算只需访问与该账期读数跨度相交的账期边界。

// spanShare 求读数跨度 [s,e)（总用量 q）归属账期 [p0,p1) 的整数用量。
//
// 归属规则（线性归属，余数进较晚账期）：
// 跨度被已存在账期边界切成若干份 d0..dk（早→晚，已覆盖部分连续）。
//   - 每份先得 floor(q*dj/(e-s))；
//   - 全部切分余量计入与跨度末端相交的最晚已存在账期（末端份）；
//   - 跨度末端尚无账期时，该跨度余量保持未归属，将来账期结算时自然取得。
//
// 已结算各账期之和恒等于其覆盖部分用量；跨度完全落在一个账期时结果就是 q。
//
// starts 为全部已存在账期起点（升序）。只做二分与 [s,e) 范围内的线性扫描，
// 扫描的边界数只与该跨度涉及的账期数有关，不随楼历史读数总量增长。
// endIsInside 表示跨度末端点 e 落在本账期内部（本份为末端份，拿全部余量）。
// 由调用方做 O(1) 邻接判断给出，不构建全量账期网格。
func spanShare(q, s, e, p0, p1 int64, endIsInside bool) int64 {
	span := e - s
	if span <= 0 {
		return 0
	}
	if min64(e, p1) <= max64(s, p0) {
		return 0
	}
	if endIsInside && p0 < e && e < p1 {
		return q - bigQuo(q, max64(s, p0)-s, span)
	}
	// 非末端份：F(份末)-F(份始)，份端点钳到 [s,e]。
	fp1 := bigQuo(q, min64(e, p1)-s, span)
	fp0 := bigQuo(q, min64(e, max64(s, p0))-s, span)
	return fp1 - fp0
}

// meterUsageInPeriod 汇总一只表与账期 [p0,p1) 相交的全部读数跨度用量。
// 第二个返回值为 false 表示读数不足以界定该账期用量（首读数晚于账期起点，
// 或账期结束时刻尚无读数锚点）。
func meterUsageInPeriod(m *Meter, p0, p1 int64, endInside func(e int64) bool) (int64, bool) {
	rs := m.readings
	if len(rs) == 0 || rs[0].Time > p0 {
		return 0, false
	}
	// 与 [p0,p1) 交叠的跨度：右端点 > p0 的第一个跨度。
	// 在跨度下标上二分条件 rs[i+1].Time > p0。
	i := sort.Search(len(rs)-1, func(i int) bool { return rs[i+1].Time > p0 })
	if i >= len(rs)-1 {
		return 0, true
	}
	var total int64
	for ; i < len(rs)-1; i++ {
		a, b := rs[i], rs[i+1]
		if a.Time >= p1 || b.Time <= p0 {
			break
		}
		if b.Time == a.Time {
			continue
		}
		total += spanShare(b.pos-a.pos, a.Time, b.Time, p0, p1, endInside(b.Time))
	}
	if rs[len(rs)-1].Time < p1 {
		return total, false
	}
	return total, true
}

// bigQuo 计算 floor(a*b/c)，乘法用 big.Int 防溢出（c>0，a,b>=0）。
func bigQuo(a, b, c int64) int64 {
	x := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return x.Quo(x, big.NewInt(c)).Int64()
}

// occupancyWeights 返回各户在账期 [p0,p1) 内的在住整数时长。
func occupancyWeights(households []*Household, p0, p1 int64) []int64 {
	w := make([]int64, len(households))
	for i, h := range households {
		for _, o := range h.occupancy {
			l, r := max64(o.Start, p0), min64(o.End, p1)
			if r > l {
				w[i] += r - l
			}
		}
	}
	return w
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// recomputed 是单个账期按当前读数序列重算的结果。
type recomputed struct {
	self        []int64
	shared      []int64
	master      int64
	sharedTotal int64
	ok          bool // 读数是否仍足以界定该账期
}

// recomputeBill 用某只表的克隆视图重算一个账期。
// meterView 把 meterID 映射到（可能被克隆/改动的）表；其余表取原表。
func (b *Building) recomputeBill(bill *Bill, view map[string]*Meter, including *Bill) (recomputed, error) {
	p0, p1 := bill.Period[0], bill.Period[1]
	// 末端份判定只需账期链表邻接：跨度末端 e 在 (p0,p1) 内，且没有
	// 已结算（或候选 including）账期的起点落在 (p0,e) 内。账期首尾相接，
	// 故唯一可能的后继起点就是 p1；including 为“正在首结的新账期”时，
	// 它的起点等于当前末账期终点。
	endInside := func(e int64) bool {
		if !(e > p0 && e < p1) {
			return false
		}
		// 正在首结（including==bill 且账单尚未入列）时，本账期就是最后一个，
		// 后继起点就是其自身终点 p1（尚不存在后继），e<p1 即在内部。
		if len(b.bills) == 0 {
			return true
		}
		next := p1
		isLast := bill == b.bills[len(b.bills)-1]
		if isLast && including != nil && including != bill {
			next = including.Period[0]
		}
		return e <= next
	}
	viewMeter := func(m *Meter) *Meter {
		if v, ok := view[m.id]; ok {
			return v
		}
		return m
	}
	n := len(b.order)
	rc := recomputed{self: make([]int64, n), shared: make([]int64, n)}
	var subSum int64
	for i, h := range b.order {
		u, ok := meterUsageInPeriod(viewMeter(h.meter), p0, p1, endInside)
		if !ok {
			return rc, nil // 读数不足：该账期无法重算（不触发更正）。
		}
		rc.self[i] = u
		subSum += u
	}
	mu, ok := meterUsageInPeriod(viewMeter(b.master), p0, p1, endInside)
	if !ok {
		return rc, nil
	}
	rc.master = mu
	rc.sharedTotal = mu - subSum
	if rc.sharedTotal < 0 {
		return rc, errf(ErrSharedNegative,
			"period [%d,%d): master %d < sum of sub-meters %d", p0, p1, mu, subSum)
	}
	nums := make([]int64, n)
	for i, h := range b.order {
		// 权重 = 面积 * 在住比例（在住时长/账期时长）；
		// 账期时长为公共分母，分摊只需分子 area*dur_i，比例为精确有理数。
		nums[i] = h.area * bill.weight[i].num
	}
	rc.shared = prorate(rc.sharedTotal, nums)
	rc.ok = true
	return rc, nil
}

// buildBill 首次结算：用当前在住记录生成权重并计算账单。
func (b *Building) buildBill(p0, p1 int64) (*Bill, error) {
	if b.master == nil || len(b.order) == 0 {
		return nil, errf(ErrInvalidArgument, "building must have a master meter and households")
	}
	bill := &Bill{
		Period: [2]int64{p0, p1},
		weight: make([]occupancyWeight, len(b.order)),
		area:   make([]int64, len(b.order)),
		ids:    make([]string, len(b.order)),
	}
	dur := p1 - p0
	durs := occupancyWeights(b.order, p0, p1)
	for i, d := range durs {
		// 保存原始在住时长（num）与账期时长（den，所有户相同），精确且便于审计。
		bill.weight[i] = occupancyWeight{num: d, den: dur}
		bill.area[i] = b.order[i].area
		bill.ids[i] = b.order[i].id
	}
	rc, err := b.recomputeBill(bill, nil, bill)
	if err != nil {
		return nil, err
	}
	if !rc.ok {
		return nil, errf(ErrInvalidArgument, "insufficient readings to settle period [%d,%d)", p0, p1)
	}
	bill.self = rc.self
	bill.share = rc.shared
	bill.master = rc.master
	bill.shared = rc.sharedTotal
	return bill, nil
}

// deferredMeters 求因新账期 [p0,p1) 结算而“结束等待”的读数跨度所在的表：
// 跨度末端 e 等于新账期终点 p1（新账期成为该跨度的末端份），
// 或 e 落在新账期内部。此前账期结算时的 floor 值此刻被最终值替代，需走更正链。
func (b *Building) deferredMeters(p0, p1 int64) map[string]*Meter {
	out := map[string]*Meter{}
	visit := func(m *Meter) {
		if m == nil {
			return
		}
		for i := 0; i+1 < len(m.readings); i++ {
			a, c := m.readings[i], m.readings[i+1]
			if c.Time == a.Time {
				continue
			}
			if c.Time > p0 && c.Time < p1 && a.Time < p1 {
				out[m.id] = m
				return
			}
			// 跨度末端恰为新账期起点：它属于更晚账期，不改变此前归属。
		}
	}
	visit(b.master)
	for _, h := range b.order {
		visit(h.meter)
	}
	return out
}

func billToSettlement(bill *Bill) *Settlement {
	s := &Settlement{Period: bill.Period, Self: map[string]int64{}, Shared: map[string]int64{}, Master: bill.master}
	for i, id := range bill.ids {
		s.Self[id] = bill.self[i]
		s.Shared[id] = bill.share[i]
	}
	return s
}

// findBill 查找完全相同的账期。
func (b *Building) findBill(p0, p1 int64) *Bill {
	i := sort.Search(len(b.bills), func(i int) bool { return b.bills[i].Period[0] >= p0 })
	if i < len(b.bills) && b.bills[i].Period[0] == p0 && b.bills[i].Period[1] == p1 {
		return b.bills[i]
	}
	return nil
}

// affectedBills 求时段 [from,to] 内/之后读数变化可能影响到的已结算账期。
// 规则：凡终点 > from 的账期，其覆盖区间内都可能存在起点 < from 的读数跨度被改变。
func (b *Building) affectedBills(from, to int64) []*Bill {
	// from 为发生变化的读数跨度起点；终点晚于 from 的账期都可能改变。
	i := sort.Search(len(b.bills), func(i int) bool { return b.bills[i].Period[1] > from })
	return append([]*Bill(nil), b.bills[i:]...)
}

// applyTrial 在克隆表上执行 mutate，再重算受影响账期：
// 任一账期重算后公摊为负则返回错误，调用方负责把原表恢复（拒绝不留痕）。
// 成功时返回各账期的新结果供提交更正使用，原表本身尚未被修改。
func (b *Building) applyTrial(m *Meter, mutate func(trial *Meter), affected []*Bill) (*Meter, []recomputed, error) {
	trial := m.clone()
	mutate(trial)
	view := map[string]*Meter{m.id: trial}
	results := make([]recomputed, len(affected))
	for i, bill := range affected {
		rc, err := b.recomputeBill(bill, view, nil)
		if err != nil {
			return nil, nil, err
		}
		if !rc.ok {
			return nil, nil, errf(ErrInvalidArgument, "settled period %v no longer well-defined", bill.Period)
		}
		results[i] = rc
	}
	return trial, results, nil
}

// commitTrial 把试算表的读数序列提交到原表（试算已验证合法）。
func (b *Building) commitTrial(m *Meter, trial *Meter) {
	m.cap = trial.cap
	m.readings = trial.readings
}

// emitCorrections 依据试算结果生成更正（账单本身保持不变）。
func (b *Building) emitCorrections(bills []*Bill, results []recomputed, reason string) {
	for i, bill := range bills {
		rc := results[i]
		c := Correction{Period: bill.Period, Reason: reason, Deltas: map[string]int64{}}
		var newShared int64
		cur := b.ensureCurrent(bill)
		for j, id := range bill.ids {
			oldPay := cur.self[j] + cur.share[j]
			newPay := rc.self[j] + rc.shared[j]
			c.Deltas[id] = newPay - oldPay
			newShared += rc.shared[j]
		}
		c.SharedDelta = newShared - sum64(cur.share)
		b.correct = append(b.correct, c)
		cur.self = rc.self
		cur.share = rc.shared
		cur.master = rc.master
		cur.sharedTotal = rc.sharedTotal
	}
}

type billCurrent struct {
	self        []int64
	share       []int64
	master      int64
	sharedTotal int64
}

func (b *Building) ensureCurrent(bill *Bill) *billCurrent {
	if b.current == nil {
		b.current = map[[2]int64]*billCurrent{}
	}
	c, ok := b.current[bill.Period]
	if !ok {
		c = &billCurrent{
			self:        append([]int64(nil), bill.self...),
			share:       append([]int64(nil), bill.share...),
			master:      bill.master,
			sharedTotal: bill.shared,
		}
		b.current[bill.Period] = c
	}
	return c
}

func sum64(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}
