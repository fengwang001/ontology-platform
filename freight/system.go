package freight

import (
	"sort"
	"sync"
)

// System 运费计算与结算系统。所有方法可并发调用，
// 通过单一互斥锁保证结果等价于某个串行顺序。
type System struct {
	mu sync.Mutex
	// contracts 按 (承运商, 线路, 等级) 索引的合同集合，
	// 集合内按生效区间排序，查找为二分，不随全系统合同总数增长。
	contracts map[contractKey]*contractSet
	// carriers 已知承运商集合（用于多承运商询价）。
	carriers map[string]struct{}
	// quotes 运单号 -> 最近一次计价明细。
	quotes map[string]FeeBreakdown
	// settlements 运单号 -> 已固化的结算明细，结算后不再改变。
	settlements map[string]FeeBreakdown
}

// NewSystem 创建空系统。
func NewSystem() *System {
	return &System{
		contracts:   make(map[contractKey]*contractSet),
		carriers:    make(map[string]struct{}),
		quotes:      make(map[string]FeeBreakdown),
		settlements: make(map[string]FeeBreakdown),
	}
}

// AddContract 添加合同。拒绝优先级：参数非法 > 区间重叠。
// 失败时不改变任何已有合同。
func (s *System) AddContract(c Contract) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateContract(&c); err != nil {
		return err
	}
	key := contractKey{carrier: c.CarrierID, lane: c.Lane, level: c.Level}
	set := s.contracts[key]
	if set == nil {
		set = &contractSet{}
		s.contracts[key] = set
	}
	if set.overlaps(c.Start, c.End) {
		return newError(ErrOverlap, "合同 %s 区间 [%d, %d) 与已有合同重叠", c.ID, c.Start, c.End)
	}
	set.add(newPricedContract(c))
	s.carriers[c.CarrierID] = struct{}{}
	return nil
}

// validateWaybill 校验运单/询价参数。
func validateWaybill(id string, carrier string, lane Lane, level ServiceLevel,
	actualWeight, volume int64, dims [3]int64) *Error {
	if id == "" {
		return newError(ErrInvalidParam, "运单号为空")
	}
	if carrier == "" {
		return newError(ErrInvalidParam, "承运商编号为空")
	}
	if lane.Origin == "" || lane.Dest == "" {
		return newError(ErrInvalidParam, "线路区域为空")
	}
	if level != Standard && level != Express {
		return newError(ErrInvalidParam, "服务等级非法: %d", level)
	}
	if actualWeight <= 0 {
		return newError(ErrInvalidParam, "实际重量必须为正: %d", actualWeight)
	}
	if volume < 0 {
		return newError(ErrInvalidParam, "体积不得为负: %d", volume)
	}
	for _, d := range dims {
		if d < 0 {
			return newError(ErrInvalidParam, "尺寸不得为负: %d", d)
		}
	}
	return nil
}

// priceLocked 按揽收时刻选择合同并计价。
// 拒绝优先级：无合同 > 时刻未覆盖 > 超出承运范围（参数校验在调用方）。
func (s *System) priceLocked(carrier string, lane Lane, level ServiceLevel, pickupTime int64,
	actualWeight, volume int64, dims [3]int64) (*FeeBreakdown, *Error) {
	key := contractKey{carrier: carrier, lane: lane, level: level}
	set := s.contracts[key]
	if set == nil || len(set.items) == 0 {
		return nil, newError(ErrNoContract, "承运商 %s 在线路 %s->%s 等级 %d 无合同", carrier, lane.Origin, lane.Dest, level)
	}
	pc := set.find(pickupTime)
	if pc == nil {
		return nil, newError(ErrTimeNotCovered, "揽收时刻 %d 不在任何合同生效区间内", pickupTime)
	}
	if outOfRange(&pc.contract, actualWeight, dims) {
		return nil, newError(ErrOutOfRange, "实际重量 %d 或单边尺寸超出承运范围", actualWeight)
	}
	bd := price(pc, actualWeight, volume, dims, lane.Dest)
	return &bd, nil
}

// Price 对运单计价（按揽收时刻选择合同），并记录该运单最近一次计价结果。
// 未结算运单重复计价使用当时最新的合同集合。
func (s *System) Price(w Waybill) (*FeeBreakdown, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateWaybill(w.ID, w.CarrierID, w.Lane, w.Level, w.ActualWeight, w.Volume, w.Dims); err != nil {
		return nil, err
	}
	bd, err := s.priceLocked(w.CarrierID, w.Lane, w.Level, w.PickupTime, w.ActualWeight, w.Volume, w.Dims)
	if err != nil {
		return nil, err
	}
	s.quotes[w.ID] = *bd
	return bd, nil
}

// Settle 结算已计价的运单：金额固化为结算时刻的计价结果。
// 同一运单号再次结算报已结算；未计价过的运单号报错。
func (s *System) Settle(waybillID string) (*FeeBreakdown, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if waybillID == "" {
		return nil, newError(ErrInvalidParam, "运单号为空")
	}
	if _, ok := s.settlements[waybillID]; ok {
		return nil, newError(ErrAlreadySettled, "运单 %s 已结算", waybillID)
	}
	bd, ok := s.quotes[waybillID]
	if !ok {
		return nil, newError(ErrNotPriced, "运单 %s 尚未计价", waybillID)
	}
	frozen := bd
	s.settlements[waybillID] = frozen
	out := frozen
	return &out, nil
}

// Settlement 查询运单的结算明细；未结算返回 false。
func (s *System) Settlement(waybillID string) (FeeBreakdown, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bd, ok := s.settlements[waybillID]
	return bd, ok
}

// QuoteAll 按线路、等级与揽收时刻对全部承运商同时询价。
// 成功的报价按总价升序、总价相同按承运商编号升序排列；
// 失败的承运商附原因列在成功报价之后（按承运商编号升序），不影响其他承运商，也不改变任何状态。
func (s *System) QuoteAll(req QuoteRequest) ([]CarrierQuote, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateWaybill("quote", "carrier", req.Lane, req.Level, req.ActualWeight, req.Volume, req.Dims); err != nil {
		return nil, err
	}
	carriers := make([]string, 0, len(s.carriers))
	for c := range s.carriers {
		carriers = append(carriers, c)
	}
	sort.Strings(carriers)

	ok := make([]CarrierQuote, 0, len(carriers))
	failed := make([]CarrierQuote, 0)
	for _, c := range carriers {
		bd, err := s.priceLocked(c, req.Lane, req.Level, req.PickupTime, req.ActualWeight, req.Volume, req.Dims)
		if err != nil {
			failed = append(failed, CarrierQuote{CarrierID: c, Reason: err})
			continue
		}
		ok = append(ok, CarrierQuote{CarrierID: c, Breakdown: bd})
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
