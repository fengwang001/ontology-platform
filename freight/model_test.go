package freight

// 本文件包含一个独立编写的朴素模型：线性扫描全部合同、逐档循环计价，
// 用于与优化实现（二分索引 + 前缀和）在大量随机操作序列上对照。

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveSystem 朴素模型：不做任何索引，所有查找均为全量线性扫描。
type naiveSystem struct {
	contracts   []Contract
	quotes      map[string]FeeBreakdown
	settlements map[string]FeeBreakdown
	carriers    map[string]bool
}

func newNaiveSystem() *naiveSystem {
	return &naiveSystem{
		quotes:      make(map[string]FeeBreakdown),
		settlements: make(map[string]FeeBreakdown),
		carriers:    make(map[string]bool),
	}
}

func naiveCeilDiv(a, b int64) int64 {
	if a%b == 0 {
		return a / b
	}
	return a/b + 1
}

func (n *naiveSystem) addContract(c Contract) *Error {
	if err := validateContract(&c); err != nil {
		return err
	}
	// 线性扫描全部合同检查重叠。
	for _, e := range n.contracts {
		if e.CarrierID == c.CarrierID && e.Lane == c.Lane && e.Level == c.Level {
			if c.Start < e.End && e.Start < c.End {
				return newError(ErrOverlap, "与合同 %s 重叠", e.ID)
			}
		}
	}
	n.contracts = append(n.contracts, c)
	n.carriers[c.CarrierID] = true
	return nil
}

// naiveFind 线性扫描找到覆盖时刻 t 的合同，并区分无合同/时刻未覆盖。
func (n *naiveSystem) naiveFind(carrier string, lane Lane, level ServiceLevel, t int64) (*Contract, *Error) {
	any := false
	for i := range n.contracts {
		c := &n.contracts[i]
		if c.CarrierID == carrier && c.Lane == lane && c.Level == level {
			any = true
			if c.Start <= t && t < c.End {
				return c, nil
			}
		}
	}
	if !any {
		return nil, newError(ErrNoContract, "无合同")
	}
	return nil, newError(ErrTimeNotCovered, "时刻未覆盖")
}

// naivePrice 逐档循环的朴素计价。
func naivePrice(c *Contract, actualWeight, volume int64, dims [3]int64, dest string) FeeBreakdown {
	volumetric := naiveCeilDiv(volume, c.VolumeDivisor)
	w := actualWeight
	if volumetric > w {
		w = volumetric
	}
	bw := naiveCeilDiv(w, c.BillingUnit) * c.BillingUnit

	// 逐档累进。
	var base int64
	for _, tier := range c.Tiers {
		if bw <= tier.Lower {
			break
		}
		upper := tier.Upper
		if bw < upper {
			upper = bw
		}
		units := (upper - tier.Lower) / c.BillingUnit
		base += units * tier.PricePerUnit
	}
	afterMin := base
	if afterMin < c.MinCharge {
		afterMin = c.MinCharge
	}
	fuel := naiveCeilDiv(afterMin*c.FuelPermille, 1000)

	var remote int64
	for _, r := range c.RemoteRegions {
		if r == dest {
			remote = c.RemoteFee
			break
		}
	}
	maxDim := dims[0]
	for _, d := range dims[1:] {
		if d > maxDim {
			maxDim = d
		}
	}
	var dimFee int64
	if maxDim > c.DimThreshold {
		dimFee = c.DimExcessFee
	}
	var weightFee int64
	if bw > c.WeightThreshold {
		weightFee = c.WeightExcessFee
	}
	return FeeBreakdown{
		ContractID:      c.ID,
		BillingWeight:   bw,
		BaseFee:         base,
		BaseAfterMin:    afterMin,
		FuelFee:         fuel,
		RemoteFee:       remote,
		DimExcessFee:    dimFee,
		WeightExcessFee: weightFee,
		Total:           afterMin + fuel + remote + dimFee + weightFee,
	}
}

func naiveOutOfRange(c *Contract, actualWeight int64, dims [3]int64) bool {
	if actualWeight > c.MaxWeight {
		return true
	}
	for _, d := range dims {
		if d > c.MaxDim {
			return true
		}
	}
	return false
}

func (n *naiveSystem) price(w Waybill) (*FeeBreakdown, *Error) {
	if err := validateWaybill(w.ID, w.CarrierID, w.Lane, w.Level, w.ActualWeight, w.Volume, w.Dims); err != nil {
		return nil, err
	}
	c, err := n.naiveFind(w.CarrierID, w.Lane, w.Level, w.PickupTime)
	if err != nil {
		return nil, err
	}
	if naiveOutOfRange(c, w.ActualWeight, w.Dims) {
		return nil, newError(ErrOutOfRange, "超出承运范围")
	}
	bd := naivePrice(c, w.ActualWeight, w.Volume, w.Dims, w.Lane.Dest)
	n.quotes[w.ID] = bd
	return &bd, nil
}

func (n *naiveSystem) settle(id string) (*FeeBreakdown, *Error) {
	if id == "" {
		return nil, newError(ErrInvalidParam, "运单号为空")
	}
	if _, ok := n.settlements[id]; ok {
		return nil, newError(ErrAlreadySettled, "已结算")
	}
	bd, ok := n.quotes[id]
	if !ok {
		return nil, newError(ErrNotPriced, "未计价")
	}
	n.settlements[id] = bd
	out := bd
	return &out, nil
}

func (n *naiveSystem) quoteAll(req QuoteRequest) ([]CarrierQuote, *Error) {
	if err := validateWaybill("quote", "carrier", req.Lane, req.Level, req.ActualWeight, req.Volume, req.Dims); err != nil {
		return nil, err
	}
	carriers := make([]string, 0, len(n.carriers))
	for c := range n.carriers {
		carriers = append(carriers, c)
	}
	sort.Strings(carriers)
	var ok, failed []CarrierQuote
	for _, carrier := range carriers {
		c, err := n.naiveFind(carrier, req.Lane, req.Level, req.PickupTime)
		if err != nil {
			failed = append(failed, CarrierQuote{CarrierID: carrier, Reason: err})
			continue
		}
		if naiveOutOfRange(c, req.ActualWeight, req.Dims) {
			failed = append(failed, CarrierQuote{CarrierID: carrier, Reason: newError(ErrOutOfRange, "超出承运范围")})
			continue
		}
		bd := naivePrice(c, req.ActualWeight, req.Volume, req.Dims, req.Lane.Dest)
		ok = append(ok, CarrierQuote{CarrierID: carrier, Breakdown: &bd})
	}
	sort.Slice(ok, func(i, j int) bool {
		a, b := ok[i].Breakdown.Total, ok[j].Breakdown.Total
		if a != b {
			return a < b
		}
		return ok[i].CarrierID < ok[j].CarrierID
	})
	return append(ok, failed...), nil
}

// ---- 随机操作序列对照 ----

var (
	modelCarriers = []string{"c0", "c1", "c2", "c3"}
	modelLanes    = []Lane{
		{Origin: "A", Dest: "B"},
		{Origin: "A", Dest: "R"},
		{Origin: "B", Dest: "C"},
	}
	modelLevels = []ServiceLevel{Standard, Express}
)

// randomContract 生成一份参数合法但区间可能重叠的合同。
func randomContract(rng *rand.Rand, seq int) Contract {
	start := int64(rng.Intn(50))
	ntiers := 1 + rng.Intn(4)
	tiers := make([]WeightTier, 0, ntiers)
	lower := int64(0)
	for i := 0; i < ntiers; i++ {
		width := int64(1+rng.Intn(20)) * 10
		upper := lower + width
		if i == ntiers-1 {
			upper = 1000000 // 最后一档封顶，保证覆盖大重量
		}
		tiers = append(tiers, WeightTier{Lower: lower, Upper: upper, PricePerUnit: int64(rng.Intn(21))})
		lower = upper
	}
	var remote []string
	if rng.Intn(2) == 0 {
		remote = append(remote, "R")
	}
	return Contract{
		ID:              fmt.Sprintf("MC-%d", seq),
		CarrierID:       modelCarriers[rng.Intn(len(modelCarriers))],
		Lane:            modelLanes[rng.Intn(len(modelLanes))],
		Level:           modelLevels[rng.Intn(len(modelLevels))],
		Start:           start,
		End:             start + 1 + int64(rng.Intn(30)),
		VolumeDivisor:   int64(1 + rng.Intn(20)),
		BillingUnit:     10,
		Tiers:           tiers,
		FuelPermille:    int64(rng.Intn(201)),
		RemoteRegions:   remote,
		RemoteFee:       int64(rng.Intn(500)),
		DimThreshold:    int64(rng.Intn(120)),
		DimExcessFee:    int64(rng.Intn(300)),
		WeightThreshold: int64(rng.Intn(400)),
		WeightExcessFee: int64(rng.Intn(300)),
		MinCharge:       int64(rng.Intn(300)),
		MaxWeight:       int64(1 + rng.Intn(600)),
		MaxDim:          int64(1 + rng.Intn(150)),
	}
}

func randomWaybill(rng *rand.Rand, seq int) Waybill {
	return Waybill{
		ID:           fmt.Sprintf("w%d", rng.Intn(20)),
		CarrierID:    modelCarriers[rng.Intn(len(modelCarriers))],
		Lane:         modelLanes[rng.Intn(len(modelLanes))],
		Level:        modelLevels[rng.Intn(len(modelLevels))],
		PickupTime:   int64(rng.Intn(80)),
		ActualWeight: int64(1 + rng.Intn(600)),
		Volume:       int64(rng.Intn(3000)),
		Dims:         [3]int64{int64(rng.Intn(120)), int64(rng.Intn(120)), int64(rng.Intn(120))},
	}
}

// opResult 记录一次操作的可比对结果，用于重放一致性校验。
type opResult struct {
	kind      string // "ok" 或错误类别
	breakdown *FeeBreakdown
	quotes    []CarrierQuote
}

func errKindOf(err *Error) string {
	if err == nil {
		return "ok"
	}
	return err.Kind.String()
}

func sameBreakdown(a, b *FeeBreakdown) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameQuotes(a, b []CarrierQuote) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].CarrierID != b[i].CarrierID {
			return false
		}
		if !sameBreakdown(a[i].Breakdown, b[i].Breakdown) {
			return false
		}
		if errKindOf(a[i].Reason) != errKindOf(b[i].Reason) {
			return false
		}
	}
	return true
}

// TestRandomOpsAgainstNaiveModel 用固定种子的随机操作序列同时驱动
// 优化实现与朴素模型，逐步比对输出；并把同一序列重放到第二个
// 优化实现实例，验证重放得到完全相同的明细。
func TestRandomOpsAgainstNaiveModel(t *testing.T) {
	const seed = 20261007
	const numOps = 4000
	rng := rand.New(rand.NewSource(seed))

	sys := NewSystem()
	replay := NewSystem()
	naive := newNaiveSystem()

	for op := 0; op < numOps; op++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // 添加合同
			c := randomContract(rng, op)
			errSys := sys.AddContract(c)
			errReplay := replay.AddContract(c)
			errNaive := naive.addContract(c)
			t.Logf("op=%d AddContract(id=%s carrier=%s lane=%v level=%d [%d,%d)) => sys=%s naive=%s",
				op, c.ID, c.CarrierID, c.Lane, c.Level, c.Start, c.End, errKindOf(errSys), errKindOf(errNaive))
			if errKindOf(errSys) != errKindOf(errNaive) || errKindOf(errSys) != errKindOf(errReplay) {
				t.Fatalf("op=%d AddContract 结果不一致: sys=%v replay=%v naive=%v", op, errSys, errReplay, errNaive)
			}
		case 4, 5, 6: // 计价
			w := randomWaybill(rng, op)
			bdSys, errSys := sys.Price(w)
			bdReplay, errReplay := replay.Price(w)
			bdNaive, errNaive := naive.price(w)
			t.Logf("op=%d Price(id=%s carrier=%s lane=%v level=%d t=%d w=%d v=%d dims=%v) => sys=%s naive=%s",
				op, w.ID, w.CarrierID, w.Lane, w.Level, w.PickupTime, w.ActualWeight, w.Volume, w.Dims,
				errKindOf(errSys), errKindOf(errNaive))
			if errKindOf(errSys) != errKindOf(errNaive) || errKindOf(errSys) != errKindOf(errReplay) {
				t.Fatalf("op=%d Price 错误类别不一致: sys=%v replay=%v naive=%v", op, errSys, errReplay, errNaive)
			}
			if !sameBreakdown(bdSys, bdNaive) || !sameBreakdown(bdSys, bdReplay) {
				t.Fatalf("op=%d Price 明细不一致:\n sys=%+v\n replay=%+v\n naive=%+v", op, bdSys, bdReplay, bdNaive)
			}
			if bdSys != nil {
				t.Logf("  明细: 合同=%s 计费重量=%d 基础=%d 提升后=%d 燃油=%d 偏远=%d 尺寸超限=%d 重量超限=%d 合计=%d",
					bdSys.ContractID, bdSys.BillingWeight, bdSys.BaseFee, bdSys.BaseAfterMin,
					bdSys.FuelFee, bdSys.RemoteFee, bdSys.DimExcessFee, bdSys.WeightExcessFee, bdSys.Total)
			}
		case 7, 8: // 结算
			id := fmt.Sprintf("w%d", rng.Intn(20))
			bdSys, errSys := sys.Settle(id)
			bdReplay, errReplay := replay.Settle(id)
			bdNaive, errNaive := naive.settle(id)
			t.Logf("op=%d Settle(id=%s) => sys=%s naive=%s", op, id, errKindOf(errSys), errKindOf(errNaive))
			if errKindOf(errSys) != errKindOf(errNaive) || errKindOf(errSys) != errKindOf(errReplay) {
				t.Fatalf("op=%d Settle 错误类别不一致: sys=%v replay=%v naive=%v", op, errSys, errReplay, errNaive)
			}
			if !sameBreakdown(bdSys, bdNaive) || !sameBreakdown(bdSys, bdReplay) {
				t.Fatalf("op=%d Settle 明细不一致:\n sys=%+v\n replay=%+v\n naive=%+v", op, bdSys, bdReplay, bdNaive)
			}
		default: // 多承运商询价
			req := QuoteRequest{
				Lane:         modelLanes[rng.Intn(len(modelLanes))],
				Level:        modelLevels[rng.Intn(len(modelLevels))],
				PickupTime:   int64(rng.Intn(80)),
				ActualWeight: int64(1 + rng.Intn(600)),
				Volume:       int64(rng.Intn(3000)),
				Dims:         [3]int64{int64(rng.Intn(120)), int64(rng.Intn(120)), int64(rng.Intn(120))},
			}
			qSys, errSys := sys.QuoteAll(req)
			qReplay, errReplay := replay.QuoteAll(req)
			qNaive, errNaive := naive.quoteAll(req)
			t.Logf("op=%d QuoteAll(lane=%v level=%d t=%d) => sys=%d条 naive=%d条",
				op, req.Lane, req.Level, req.PickupTime, len(qSys), len(qNaive))
			if errKindOf(errSys) != errKindOf(errNaive) || errKindOf(errSys) != errKindOf(errReplay) {
				t.Fatalf("op=%d QuoteAll 错误不一致: %v %v %v", op, errSys, errReplay, errNaive)
			}
			if !sameQuotes(qSys, qNaive) || !sameQuotes(qSys, qReplay) {
				t.Fatalf("op=%d QuoteAll 结果不一致:\n sys=%+v\n replay=%+v\n naive=%+v", op, qSys, qReplay, qNaive)
			}
		}
	}
	t.Logf("随机对照完成: seed=%d ops=%d，优化实现、重放实例与朴素模型输出完全一致", seed, numOps)
}
