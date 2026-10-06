// Package system 是对外门面：并发安全地串联合同存储、计价引擎与结算台账，
// 实现拒绝优先级、多承运商询价与可重放的操作日志。
//
// 并发模型：所有状态变更与计价/结算在单把 sync.RWMutex 下原子完成，
// 因此任意并发交错的结果都等价于这些操作的某个串行排列；
// “同一运单在任一串行位置上至多结算一次”由台账在临界区内判定保证。
package system

import (
	"sort"
	"sync"

	"ontology/freight/ledger"
	"ontology/freight/model"
	"ontology/freight/pricing"
	"ontology/freight/store"
)

// Logger 操作日志接口。输出只包含操作序号、输入、输出与判定依据，
// 不含墙钟时间，保证相同操作序列重放产生完全相同的日志。
type Logger interface {
	Log(seq int64, op string, input, output string)
}

// System 运费计算与结算系统。
type System struct {
	mu     sync.RWMutex
	store  *store.Store
	ledger *ledger.Ledger
	logger Logger
	seq    int64
}

// New 创建系统。logger 可为 nil（表示不打印日志）。
func New(logger Logger) *System {
	return &System{store: store.New(), ledger: ledger.New(), logger: logger}
}

func (s *System) logLocked(op, input, output string) {
	if s.logger == nil {
		return
	}
	s.seq++
	s.logger.Log(s.seq, op, input, output)
}

// AddContract 新增或修订合同。拒绝优先级：参数非法 → 区间重叠。
func (s *System) AddContract(c *model.Contract) error {
	if err := c.Validate(); err != nil {
		s.record("add_contract", contractLog(c), err)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.store.Add(c)
	s.logLocked("add_contract", contractLog(c), resultLog(nil, err))
	return err
}

// record 在未加写锁的参数失败路径上也保证日志序号互斥。
func (s *System) record(op, input string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logLocked(op, input, resultLog(nil, err))
}

// Price 对运单计价。拒绝优先级：参数非法 → 无合同 → 时刻未覆盖 → 超出承运范围。
// 计价使用运单揽收时刻选择合同，而非调用时刻。
func (s *System) Price(w *model.Waybill) (*model.FeeBreakdown, error) {
	if err := w.Validate(); err != nil {
		s.record("price", waybillLog(w), err)
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	fb, err := s.priceLocked(w)
	s.logLocked("price", waybillLog(w), resultLog(fb, err))
	return fb, err
}

// priceLocked 在写锁内执行完整计价链。
func (s *System) priceLocked(w *model.Waybill) (*model.FeeBreakdown, error) {
	// 拒绝优先级：无合同 → 时刻未覆盖 → 超出承运范围（参数非法已在调用前判定）。
	// 由于按承运商计价，运单必须显式携带承运商编号（见 model.Waybill.CarrierID）。
	c := s.store.Find(w.CarrierID, w.Route, w.Class, w.PickupAt)
	if c == nil {
		if s.store.LaneExists(w.CarrierID, w.Route, w.Class) {
			return nil, model.NewError(model.CodeTimeNotCovered,
				"揽收时刻 %d 不在承运商 %s 的任何生效区间内", w.PickupAt, w.CarrierID)
		}
		return nil, model.NewError(model.CodeNoContract,
			"承运商 %s 的线路 %s→%s（%s）无任何合同",
			w.CarrierID, w.Route.From, w.Route.To, w.Class)
	}
	if !pricing.InCarrierRange(w, c) {
		return nil, model.NewError(model.CodeOutOfRange,
			"实际重量 %d 或单边尺寸超出承运商 %s 的承运范围", w.Weight, w.CarrierID)
	}
	fb, err := pricing.Calculate(w, c)
	if err != nil {
		return nil, err
	}
	s.ledger.SavePricing(fb)
	return fb, nil
}

// Settle 对已计价运单结算一次；重复结算报已结算。
func (s *System) Settle(waybillNumber string) (*model.FeeBreakdown, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fb, err := s.ledger.Settle(waybillNumber)
	s.logLocked("settle", toJSON(map[string]string{"waybill_number": waybillNumber}),
		resultLog(fb, err))
	return fb, err
}

// Settlement 查询固化的结算明细；未结算返回 nil。
func (s *System) Settlement(waybillNumber string) *model.FeeBreakdown {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ledger.Settlement(waybillNumber)
}

// QuoteLine 多承运商询价结果中的一条。
type QuoteLine struct {
	CarrierID string              `json:"carrier_id"`
	Total     int64               `json:"total"`
	Breakdown *model.FeeBreakdown `json:"breakdown,omitempty"`
	Reason    model.Code          `json:"reason,omitempty"`
}

// QuoteRequest 多承运商询价输入。Carriers 为空表示对该线路全部已知承运商询价。
type QuoteRequest struct {
	Route    model.Route
	Class    model.ServiceClass
	PickupAt model.Time
	Weight   int64
	Dim      model.Dimensions
	Carriers []string
}

// Quote 不改任何状态；成功报价按总价、承运商编号升序，失败项单列在后。
// 单个承运商失败（无合同/时刻未覆盖/超范围/参数非法）只记录其原因，不影响其他承运商。
func (s *System) Quote(req QuoteRequest) ([]QuoteLine, error) {
	tmp := model.Waybill{
		Number:    "__quote__",
		CarrierID: "__quote__",
		Route:     req.Route,
		Class:     req.Class,
		PickupAt:  req.PickupAt,
		Weight:    req.Weight,
		Dim:       req.Dim,
	}
	if err := tmp.Validate(); err != nil {
		s.record("quote", quoteLog(req), err)
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	carriers := append([]string(nil), req.Carriers...)
	if len(carriers) == 0 {
		carriers = s.store.Carriers(req.Route, req.Class)
	} else {
		sort.Strings(carriers)
		carriers = dedup(carriers)
	}

	ok := make([]QuoteLine, 0, len(carriers))
	fail := make([]QuoteLine, 0)
	for _, id := range carriers {
		line := s.quoteOneLocked(&tmp, id)
		if line.Reason == "" {
			ok = append(ok, line)
		} else {
			fail = append(fail, line)
		}
	}
	sort.Slice(ok, func(i, j int) bool {
		if ok[i].Total != ok[j].Total {
			return ok[i].Total < ok[j].Total
		}
		return ok[i].CarrierID < ok[j].CarrierID
	})
	sort.Slice(fail, func(i, j int) bool { return fail[i].CarrierID < fail[j].CarrierID })
	lines := append(ok, fail...)
	s.logLocked("quote", quoteLog(req), quoteResultLog(lines))
	return lines, nil
}

// quoteOneLocked 对单一承运商询价；任何失败都折叠为 QuoteLine.Reason，绝不改变状态。
func (s *System) quoteOneLocked(w *model.Waybill, carrier string) QuoteLine {
	line := QuoteLine{CarrierID: carrier}
	c := s.store.Find(carrier, w.Route, w.Class, w.PickupAt)
	if c == nil {
		if s.store.LaneExists(carrier, w.Route, w.Class) {
			line.Reason = model.CodeTimeNotCovered
		} else {
			line.Reason = model.CodeNoContract
		}
		return line
	}
	if !pricing.InCarrierRange(w, c) {
		line.Reason = model.CodeOutOfRange
		return line
	}
	fb, err := pricing.Calculate(w, c)
	if err != nil {
		line.Reason = model.CodeOf(err)
		if line.Reason == "" {
			line.Reason = model.CodeInvalidArgument
		}
		return line
	}
	line.Breakdown = fb
	line.Total = fb.Total
	return line
}

func dedup(in []string) []string {
	out := in[:0]
	for i, v := range in {
		if i > 0 && v == in[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}
