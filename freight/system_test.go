package freight

import (
	"sync"
	"testing"
)

// baseContract 返回一份便于手算的合同，各测试按需修改字段。
// 阶梯: [0,100)@10分, [100,300)@5分, [300,1e6)@2分；计重单位 10；燃油 10%。
func baseContract() Contract {
	return Contract{
		ID:            "C1",
		CarrierID:     "carrier-1",
		Lane:          Lane{Origin: "A", Dest: "B"},
		Level:         Standard,
		Start:         10,
		End:           20,
		VolumeDivisor: 10,
		BillingUnit:   10,
		Tiers: []WeightTier{
			{Lower: 0, Upper: 100, PricePerUnit: 10},
			{Lower: 100, Upper: 300, PricePerUnit: 5},
			{Lower: 300, Upper: 1000000, PricePerUnit: 2},
		},
		FuelPermille:    100,
		RemoteRegions:   []string{"R"},
		RemoteFee:       500,
		DimThreshold:    50,
		DimExcessFee:    300,
		WeightThreshold: 200,
		WeightExcessFee: 700,
		MinCharge:       120,
		MaxWeight:       10000,
		MaxDim:          200,
	}
}

// defaultWaybill 默认运单：实际重量 50、体积 0、尺寸 10x10x10。
func defaultWaybill(id string, pickup int64) Waybill {
	return Waybill{
		ID:           id,
		CarrierID:    "carrier-1",
		Lane:         Lane{Origin: "A", Dest: "B"},
		Level:        Standard,
		PickupTime:   pickup,
		ActualWeight: 50,
		Dims:         [3]int64{10, 10, 10},
	}
}

func mustAdd(t *testing.T, s *System, c Contract) {
	t.Helper()
	if err := s.AddContract(c); err != nil {
		t.Fatalf("AddContract(%s) 失败: %v", c.ID, err)
	}
}

func mustPrice(t *testing.T, s *System, w Waybill) *FeeBreakdown {
	t.Helper()
	bd, err := s.Price(w)
	if err != nil {
		t.Fatalf("Price(%s) 失败: %v", w.ID, err)
	}
	return bd
}

func expectErrKind(t *testing.T, err *Error, kind ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际成功", kind)
	}
	if err.Kind != kind {
		t.Fatalf("期望错误 %s，实际 %v", kind, err)
	}
}

// 生效区间左闭右开：起点取等命中，终点取等未覆盖。
func TestEffectiveIntervalBounds(t *testing.T) {
	s := NewSystem()
	mustAdd(t, s, baseContract())

	if bd := mustPrice(t, s, defaultWaybill("w-start", 10)); bd.ContractID != "C1" {
		t.Fatalf("揽收时刻=Start 应命中合同 C1，实际 %s", bd.ContractID)
	}
	_, err := s.Price(defaultWaybill("w-end", 20))
	expectErrKind(t, err, ErrTimeNotCovered)
	_, err = s.Price(defaultWaybill("w-before", 9))
	expectErrKind(t, err, ErrTimeNotCovered)
}

// 无合同与时刻未覆盖必须可区分。
func TestNoContractVsTimeNotCovered(t *testing.T) {
	s := NewSystem()
	mustAdd(t, s, baseContract())

	w := defaultWaybill("w1", 15)
	w.Lane = Lane{Origin: "A", Dest: "Z"} // 无合同的线路
	_, err := s.Price(w)
	expectErrKind(t, err, ErrNoContract)

	w2 := defaultWaybill("w2", 99) // 有合同但时刻未覆盖
	_, err = s.Price(w2)
	expectErrKind(t, err, ErrTimeNotCovered)
}

// 相接合同允许添加，且边界时刻归属右侧合同。
func TestAdjacentContracts(t *testing.T) {
	s := NewSystem()
	c1 := baseContract()
	c2 := baseContract()
	c2.ID = "C2"
	c2.Start, c2.End = 20, 30
	c2.MinCharge = 999 // 用最低收费区分两份合同
	mustAdd(t, s, c1)
	mustAdd(t, s, c2)

	if bd := mustPrice(t, s, defaultWaybill("w1", 19)); bd.ContractID != "C1" {
		t.Fatalf("t=19 应命中 C1，实际 %s", bd.ContractID)
	}
	bd := mustPrice(t, s, defaultWaybill("w2", 20))
	if bd.ContractID != "C2" {
		t.Fatalf("t=20 应命中 C2，实际 %s", bd.ContractID)
	}
	if bd.BaseAfterMin != 999 {
		t.Fatalf("t=20 应使用 C2 的最低收费 999，实际 %d", bd.BaseAfterMin)
	}
}

// 区间重叠拒绝：完全包含、部分重叠均拒绝，且已有合同不受影响。
func TestOverlapRejected(t *testing.T) {
	s := NewSystem()
	mustAdd(t, s, baseContract()) // [10,20)

	for _, tc := range []struct {
		name       string
		start, end int64
	}{
		{"完全包含", 5, 25},
		{"左重叠", 5, 15},
		{"右重叠", 15, 25},
		{"被包含", 12, 18},
		{"同区间", 10, 20},
	} {
		c := baseContract()
		c.ID = "X"
		c.Start, c.End = tc.start, tc.end
		err := s.AddContract(c)
		expectErrKind(t, err, ErrOverlap)
	}
	// 已有合同仍可正常使用。
	if bd := mustPrice(t, s, defaultWaybill("w1", 15)); bd.ContractID != "C1" {
		t.Fatalf("重叠拒绝后已有合同应不受影响，实际命中 %s", bd.ContractID)
	}
	// 不同承运商/线路/等级的同区间合同互不干扰。
	c := baseContract()
	c.ID = "C-other-carrier"
	c.CarrierID = "carrier-2"
	mustAdd(t, s, c)
	c = baseContract()
	c.ID = "C-other-level"
	c.Level = Express
	mustAdd(t, s, c)
	c = baseContract()
	c.ID = "C-other-lane"
	c.Lane = Lane{Origin: "A", Dest: "C"}
	mustAdd(t, s, c)
}

// 合同参数非法优先于区间重叠。
func TestAddContractPriority(t *testing.T) {
	s := NewSystem()
	mustAdd(t, s, baseContract())
	c := baseContract()
	c.Start, c.End = 15, 25 // 与已有合同重叠
	c.VolumeDivisor = 0     // 同时参数非法
	err := s.AddContract(c)
	expectErrKind(t, err, ErrInvalidParam)
}

// 体积重量与实际重量互换：两种主导情况分别验证。
func TestVolumetricVsActual(t *testing.T) {
	s := NewSystem()
	mustAdd(t, s, baseContract())

	// 实际重量主导：实际 500，体积 100 -> 体积重量 10，计费重量 500。
	w := defaultWaybill("w-actual", 15)
	w.ActualWeight, w.Volume = 500, 100
	if bd := mustPrice(t, s, w); bd.BillingWeight != 500 {
		t.Fatalf("实际重量主导时计费重量应为 500，实际 %d", bd.BillingWeight)
	}
	// 体积重量主导：实际 50，体积 1000 -> 体积重量 100，计费重量 100。
	w = defaultWaybill("w-volume", 15)
	w.ActualWeight, w.Volume = 50, 1000
	if bd := mustPrice(t, s, w); bd.BillingWeight != 100 {
		t.Fatalf("体积重量主导时计费重量应为 100，实际 %d", bd.BillingWeight)
	}
}

// 向上取整边界：体积折算与计重单位取整的整除/余 1 两侧。
func TestCeilBoundaries(t *testing.T) {
	s := NewSystem()
	c := baseContract()
	c.WeightThreshold = 1000000 // 关闭重量超限，聚焦取整
	mustAdd(t, s, c)

	// 体积 100 / 系数 10 = 10 整除不进位；101 -> 11，再取整到计重单位 20。
	w := defaultWaybill("w-exact", 15)
	w.ActualWeight, w.Volume = 1, 100
	if bd := mustPrice(t, s, w); bd.BillingWeight != 10 {
		t.Fatalf("体积整除时计费重量应为 10，实际 %d", bd.BillingWeight)
	}
	w = defaultWaybill("w-plus1", 15)
	w.ActualWeight, w.Volume = 1, 101
	if bd := mustPrice(t, s, w); bd.BillingWeight != 20 {
		t.Fatalf("体积余 1 应先得 11 再取整到 20，实际 %d", bd.BillingWeight)
	}
	// 计重单位取整：实际 100 整除不变；101 -> 110。
	w = defaultWaybill("w-unit-exact", 15)
	w.ActualWeight, w.Volume = 100, 0
	if bd := mustPrice(t, s, w); bd.BillingWeight != 100 {
		t.Fatalf("计重单位整除时应为 100，实际 %d", bd.BillingWeight)
	}
	w = defaultWaybill("w-unit-plus1", 15)
	w.ActualWeight, w.Volume = 101, 0
	if bd := mustPrice(t, s, w); bd.BillingWeight != 110 {
		t.Fatalf("计重单位余 1 应取整到 110，实际 %d", bd.BillingWeight)
	}
}

// 跨多档累进：各档内部分分别计价，而非整体按所落档单价。
func TestProgressiveTiers(t *testing.T) {
	s := NewSystem()
	c := baseContract()
	c.MinCharge = 0
	c.FuelPermille = 0
	c.WeightThreshold = 1000000
	mustAdd(t, s, c)

	// 计费重量 250：10*10 + 15*5 = 175（若整体按 5 分档则为 125）。
	w := defaultWaybill("w-250", 15)
	w.ActualWeight = 250
	bd := mustPrice(t, s, w)
	if bd.BaseFee != 175 {
		t.Fatalf("计费重量 250 累进基础运费应为 175，实际 %d", bd.BaseFee)
	}
	// 计费重量 400：10*10 + 20*5 + 10*2 = 220。
	w = defaultWaybill("w-400", 15)
	w.ActualWeight = 400
	bd = mustPrice(t, s, w)
	if bd.BaseFee != 220 {
		t.Fatalf("计费重量 400 累进基础运费应为 220，实际 %d", bd.BaseFee)
	}
	// 档边界 300：10*10 + 20*5 = 200，恰好不进入第三档。
	w = defaultWaybill("w-300", 15)
	w.ActualWeight = 300
	bd = mustPrice(t, s, w)
	if bd.BaseFee != 200 {
		t.Fatalf("计费重量 300 基础运费应为 200，实际 %d", bd.BaseFee)
	}
}

// 最低收费：低于时提升；恰等于基础运费时不变；燃油作用于提升后的金额并向上取整。
func TestMinChargeAndFuel(t *testing.T) {
	s := NewSystem()
	c := baseContract()
	c.MinCharge = 175 // 恰等于计费重量 250 的累进基础运费
	c.WeightThreshold = 1000000
	mustAdd(t, s, c)

	// 恰等于：base=175，提升后仍 175；燃油 ceil(175*100/1000)=18。
	w := defaultWaybill("w-eq", 15)
	w.ActualWeight = 250
	bd := mustPrice(t, s, w)
	if bd.BaseFee != 175 || bd.BaseAfterMin != 175 {
		t.Fatalf("最低收费恰等于基础运费时不应提升: base=%d after=%d", bd.BaseFee, bd.BaseAfterMin)
	}
	if bd.FuelFee != 18 {
		t.Fatalf("燃油应向上取整为 18，实际 %d", bd.FuelFee)
	}
	if bd.Total != 193 {
		t.Fatalf("合计应为 193，实际 %d", bd.Total)
	}

	// 低于最低收费：计费重量 50 -> base=5*10=50，提升到 175。
	w = defaultWaybill("w-below", 15)
	w.ActualWeight = 50
	bd = mustPrice(t, s, w)
	if bd.BaseFee != 50 || bd.BaseAfterMin != 175 {
		t.Fatalf("低于最低收费应提升: base=%d after=%d", bd.BaseFee, bd.BaseAfterMin)
	}
	// 燃油作用于提升后的 175 而非 50。
	if bd.FuelFee != 18 {
		t.Fatalf("燃油应作用于提升后金额: %d", bd.FuelFee)
	}

	// 燃油整除边界：提升后 170 -> 燃油恰 17。
	s2 := NewSystem()
	c2 := baseContract()
	c2.MinCharge = 170
	c2.WeightThreshold = 1000000
	mustAdd(t, s2, c2)
	w = defaultWaybill("w-fuel-exact", 15)
	w.ActualWeight = 50
	bd = mustPrice(t, s2, w)
	if bd.FuelFee != 17 {
		t.Fatalf("燃油整除时应为 17，实际 %d", bd.FuelFee)
	}
}

// 偏远附加费：仅终点区域在清单内时收取。
func TestRemoteFee(t *testing.T) {
	s := NewSystem()
	c := baseContract()
	c.MinCharge = 0
	c.FuelPermille = 0
	c.WeightThreshold = 1000000
	mustAdd(t, s, c)
	cr := c // 覆盖偏远线路 A->R 的合同
	cr.ID = "C-remote"
	cr.Lane.Dest = "R"
	mustAdd(t, s, cr)

	w := defaultWaybill("w-remote", 15)
	w.Lane.Dest = "R"
	bd := mustPrice(t, s, w)
	if bd.RemoteFee != 500 {
		t.Fatalf("终点为偏远区域应收 500，实际 %d", bd.RemoteFee)
	}
	if bd.Total != bd.BaseAfterMin+500 {
		t.Fatalf("合计应含偏远附加费: %d", bd.Total)
	}
	w = defaultWaybill("w-not-remote", 15)
	if bd := mustPrice(t, s, w); bd.RemoteFee != 0 {
		t.Fatalf("终点非偏远区域不应收取，实际 %d", bd.RemoteFee)
	}
}

// 两项超限附加独立成立：各自成立各自加收，同时成立两项都收。
func TestExcessFeesIndependent(t *testing.T) {
	s := NewSystem()
	c := baseContract()
	c.MinCharge = 0
	c.FuelPermille = 0
	mustAdd(t, s, c)

	// 仅尺寸超限：单边 51 > 50，计费重量 50 <= 200。
	w := defaultWaybill("w-dim", 15)
	w.Dims = [3]int64{51, 10, 10}
	bd := mustPrice(t, s, w)
	if bd.DimExcessFee != 300 || bd.WeightExcessFee != 0 {
		t.Fatalf("仅尺寸超限: dim=%d weight=%d", bd.DimExcessFee, bd.WeightExcessFee)
	}
	// 仅重量超限：计费重量 250 > 200，尺寸正常。
	w = defaultWaybill("w-weight", 15)
	w.ActualWeight = 250
	bd = mustPrice(t, s, w)
	if bd.DimExcessFee != 0 || bd.WeightExcessFee != 700 {
		t.Fatalf("仅重量超限: dim=%d weight=%d", bd.DimExcessFee, bd.WeightExcessFee)
	}
	// 同时成立：两项都收。
	w = defaultWaybill("w-both", 15)
	w.ActualWeight = 250
	w.Dims = [3]int64{51, 10, 10}
	bd = mustPrice(t, s, w)
	if bd.DimExcessFee != 300 || bd.WeightExcessFee != 700 {
		t.Fatalf("同时超限应两项都收: dim=%d weight=%d", bd.DimExcessFee, bd.WeightExcessFee)
	}
	// 阈值取等不收：单边恰 50、计费重量恰 200。
	w = defaultWaybill("w-equal", 15)
	w.ActualWeight = 200
	w.Dims = [3]int64{50, 10, 10}
	bd = mustPrice(t, s, w)
	if bd.DimExcessFee != 0 || bd.WeightExcessFee != 0 {
		t.Fatalf("阈值取等不应收取: dim=%d weight=%d", bd.DimExcessFee, bd.WeightExcessFee)
	}
}

// 超出承运范围：判定在计费之前，且使用实际重量而非计费重量。
func TestOutOfRange(t *testing.T) {
	s := NewSystem()
	c := baseContract()
	c.MaxWeight = 100
	c.MaxDim = 60
	mustAdd(t, s, c)

	// 实际重量 100 未超，但体积重量使计费重量达 5000：仍应正常计价。
	w := defaultWaybill("w-heavy-billing", 15)
	w.ActualWeight = 100
	w.Volume = 500000 // 体积重量 50000
	bd := mustPrice(t, s, w)
	if bd.BillingWeight != 50000 {
		t.Fatalf("计费重量应为 50000，实际 %d", bd.BillingWeight)
	}
	// 实际重量 101 > 100：超出承运范围。
	w = defaultWaybill("w-heavy-actual", 15)
	w.ActualWeight = 101
	_, err := s.Price(w)
	expectErrKind(t, err, ErrOutOfRange)
	// 单边 61 > 60：超出承运范围。
	w = defaultWaybill("w-long", 15)
	w.Dims = [3]int64{61, 10, 10}
	_, err = s.Price(w)
	expectErrKind(t, err, ErrOutOfRange)
	// 边界取等：实际重量恰 100、单边恰 60 均可承运。
	w = defaultWaybill("w-edge", 15)
	w.ActualWeight = 100
	w.Dims = [3]int64{60, 60, 60}
	mustPrice(t, s, w)
}

// 拒绝优先级：参数非法 > 无合同 > 时刻未覆盖 > 超出承运范围。
func TestRefusalPriority(t *testing.T) {
	s := NewSystem()
	c := baseContract()
	c.MaxWeight = 100
	mustAdd(t, s, c)

	// 参数非法 + 无合同：报参数非法。
	w := defaultWaybill("", 15)
	w.Lane.Dest = "Z"
	_, err := s.Price(w)
	expectErrKind(t, err, ErrInvalidParam)

	// 无合同 + 超出承运范围：报无合同。
	w = defaultWaybill("w1", 15)
	w.Lane.Dest = "Z"
	w.ActualWeight = 100000
	_, err = s.Price(w)
	expectErrKind(t, err, ErrNoContract)

	// 时刻未覆盖 + 超出承运范围：报时刻未覆盖。
	w = defaultWaybill("w2", 99)
	w.ActualWeight = 100000
	_, err = s.Price(w)
	expectErrKind(t, err, ErrTimeNotCovered)

	// 仅超出承运范围。
	w = defaultWaybill("w3", 15)
	w.ActualWeight = 101
	_, err = s.Price(w)
	expectErrKind(t, err, ErrOutOfRange)
}

// 结算：金额固化；重复结算报已结算；未计价运单结算报错；
// 结算后新增合同不改变已结算金额；未结算运单再次计价使用最新合同集合。
func TestSettlement(t *testing.T) {
	s := NewSystem()
	mustAdd(t, s, baseContract()) // [10,20)

	// 未计价运单结算报错。
	_, err := s.Settle("ghost")
	expectErrKind(t, err, ErrNotPriced)

	// 计价并结算。
	bd1 := mustPrice(t, s, defaultWaybill("w1", 15))
	settled, err := s.Settle("w1")
	if err != nil {
		t.Fatalf("Settle 失败: %v", err)
	}
	if *settled != *bd1 {
		t.Fatalf("结算金额应等于计价结果: %+v vs %+v", settled, bd1)
	}

	// 重复结算报已结算。
	_, err = s.Settle("w1")
	expectErrKind(t, err, ErrAlreadySettled)

	// 未结算运单 w2 揽收时刻 25 落在合同间隙：时刻未覆盖。
	w2 := defaultWaybill("w2", 25)
	_, err = s.Price(w2)
	expectErrKind(t, err, ErrTimeNotCovered)

	// 结算后新增合同（相接区间），已结算金额不变。
	c2 := baseContract()
	c2.ID = "C2"
	c2.Start, c2.End = 20, 30
	c2.MinCharge = 99999
	mustAdd(t, s, c2)
	got, ok := s.Settlement("w1")
	if !ok || got != *bd1 {
		t.Fatalf("已结算金额不得被合同变更改写: %+v", got)
	}

	// 未结算运单再次计价使用最新合同集合：新增 C2 后 w2 可计价且命中 C2。
	bd2 := mustPrice(t, s, w2)
	if bd2.ContractID != "C2" || bd2.BaseAfterMin != 99999 {
		t.Fatalf("再次计价应使用最新合同 C2: %+v", bd2)
	}
}

// 多承运商询价：按总价升序、同价按承运商编号升序；
// 失败承运商单列原因且不影响其他报价，也不改变任何状态。
func TestQuoteAll(t *testing.T) {
	s := NewSystem()
	lane := Lane{Origin: "A", Dest: "B"}

	mk := func(id, carrier string, mutate func(*Contract)) {
		c := baseContract()
		c.ID, c.CarrierID, c.Lane = id, carrier, lane
		c.MinCharge, c.FuelPermille, c.WeightThreshold = 0, 0, 1000000
		if mutate != nil {
			mutate(&c)
		}
		mustAdd(t, s, c)
	}
	// c-mid: 总价 50（计费重量 50 -> 5*10）。
	mk("q1", "c-mid", nil)
	// c-cheap: 总价 30。
	mk("q2", "c-cheap", func(c *Contract) { c.Tiers[0].PricePerUnit = 6 })
	// c-tie: 与 c-cheap 同价 30，编号更大，应排在其后。
	mk("q3", "c-tie", func(c *Contract) { c.Tiers[0].PricePerUnit = 6 })
	// c-nocover: 合同区间不覆盖揽收时刻。
	mk("q4", "c-nocover", func(c *Contract) { c.Start, c.End = 100, 200 })
	// c-limited: 超出承运范围。
	mk("q5", "c-limited", func(c *Contract) { c.MaxWeight = 10 })
	// c-elsewhere: 只有其他线路的合同 -> 无合同。
	mk("q6", "c-elsewhere", func(c *Contract) { c.Lane = Lane{Origin: "X", Dest: "Y"} })

	quotes, qerr := s.QuoteAll(QuoteRequest{
		Lane: lane, Level: Standard, PickupTime: 15,
		ActualWeight: 50, Dims: [3]int64{10, 10, 10},
	})
	if qerr != nil {
		t.Fatalf("QuoteAll 失败: %v", qerr)
	}
	if len(quotes) != 6 {
		t.Fatalf("应返回 6 个承运商结果，实际 %d", len(quotes))
	}
	// 成功报价排序：c-cheap(30), c-tie(30), c-mid(50)。
	wantOK := []struct {
		carrier string
		total   int64
	}{{"c-cheap", 30}, {"c-tie", 30}, {"c-mid", 50}}
	for i, w := range wantOK {
		q := quotes[i]
		if q.CarrierID != w.carrier || q.Breakdown == nil || q.Breakdown.Total != w.total {
			t.Fatalf("报价[%d] 应为 %s 总价 %d，实际 %+v", i, w.carrier, w.total, q)
		}
	}
	// 失败承运商按编号升序单列原因。
	wantFail := []struct {
		carrier string
		kind    ErrKind
	}{{"c-elsewhere", ErrNoContract}, {"c-limited", ErrOutOfRange}, {"c-nocover", ErrTimeNotCovered}}
	for i, w := range wantFail {
		q := quotes[3+i]
		if q.CarrierID != w.carrier || q.Reason == nil || q.Reason.Kind != w.kind || q.Breakdown != nil {
			t.Fatalf("失败[%d] 应为 %s/%s，实际 %+v", i, w.carrier, w.kind, q)
		}
	}
	// 询价不改变任何状态：运单号未被记录，结算报错。
	if _, err := s.Settle("anything"); err == nil || err.Kind != ErrNotPriced {
		t.Fatalf("询价不应产生可结算记录: %v", err)
	}
}

// 并发调用：多 goroutine 混合计价/结算/加合同，依赖 -race 检测，
// 并验证同一运单在并发下至多结算一次。
func TestConcurrentSettleOnce(t *testing.T) {
	s := NewSystem()
	mustAdd(t, s, baseContract())
	mustPrice(t, s, defaultWaybill("hot", 15))

	const workers = 16
	var wg sync.WaitGroup
	successes := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Settle("hot"); err == nil {
				successes <- "ok"
			}
			// 并发计价与询价不应 panic 或数据竞争。
			s.Price(defaultWaybill("bg", 15))
			s.QuoteAll(QuoteRequest{
				Lane: Lane{Origin: "A", Dest: "B"}, Level: Standard,
				PickupTime: 15, ActualWeight: 50, Dims: [3]int64{1, 1, 1},
			})
		}(i)
	}
	wg.Wait()
	close(successes)
	n := 0
	for range successes {
		n++
	}
	if n != 1 {
		t.Fatalf("同一运单并发结算应恰好成功一次，实际 %d 次", n)
	}
}
