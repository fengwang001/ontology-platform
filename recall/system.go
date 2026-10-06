package recall

import (
	"sort"
	"sync"
)

// 操作名常量，用于错误与日志。
const (
	opInbound  = "入库"
	opTransfer = "调拨"
	opDispense = "发放"
	opReturn   = "退药"
	opRegister = "召回登记"
	opRelease  = "召回解除"
	opQuery    = "批次查询"
	opRecovery = "追回清单"
)

// System 是召回与追溯锁定系统的唯一对外入口。
//
// 单一互斥锁把所有操作串行化，使并发执行严格等价于某个串行顺序；
// 操作按"参数非法 → 时钟回退 → 对象不存在 → 召回禁止 → 需知情确认 →
// 库存不足 → 退药超量 → 状态不符"的固定优先级判定，任一拒绝都发生在
// 状态变更之前，因此被拒绝的操作不改变任何库存、发放记录、召回登记与时钟。
type System struct {
	mu      sync.Mutex
	lastNow int64
	inv     *inventoryModule
	recs    *recallModule
}

// New 创建空系统。
func New() *System {
	return &System{inv: newInventory(), recs: newRecalls()}
}

// checkClock 校验单调时钟。必须在参数校验之后、业务判定之前调用。
func (s *System) checkClock(op string, now int64) error {
	if now < s.lastNow {
		return opError(CodeClockRollback, op, "now 小于上一次被接受操作的时刻")
	}
	return nil
}

func validID(id string) bool { return id != "" }

func validQty(q int) bool { return minQty <= q && q <= maxQty }

// Inbound 入库登记：药品进入药库；同一药品批号不得重复入库。
func (s *System) Inbound(req InboundReq) error {
	if !validID(req.DrugID) || !validID(req.BatchID) || !validQty(req.Quantity) {
		return opError(CodeInvalidParam, opInbound, "药品/批号须为非空串，数量须在 [1,10^6]")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opInbound, req.Now); err != nil {
		return err
	}
	if s.inv.hasBatch(req.DrugID, req.BatchID) {
		return opError(CodeInvalidState, opInbound, "同一药品批号不得重复入库")
	}
	s.inv.inbound(req.DrugID, req.BatchID, req.Quantity)
	s.lastNow = req.Now
	return nil
}

// Transfer 调拨：在两个位置之间移动某批次数量。
func (s *System) Transfer(req TransferReq) error {
	if !validID(req.DrugID) || !validID(req.BatchID) ||
		!validID(req.From) || !validID(req.To) || !validQty(req.Quantity) {
		return opError(CodeInvalidParam, opTransfer, "标识须为非空串，数量须在 [1,10^6]")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opTransfer, req.Now); err != nil {
		return err
	}
	if !s.inv.hasBatch(req.DrugID, req.BatchID) {
		return opError(CodeNotFound, opTransfer, "批次不存在")
	}
	level, _ := s.recs.effective(req.DrugID, req.BatchID)
	l := s.inv.ledger(req.DrugID, req.BatchID)
	// 一级：禁止一切调拨；二级：调拨只允许调入药库。
	if level == 1 {
		return opError(CodeRecallForbidden, opTransfer, "一级召回：库存冻结，禁止一切调拨")
	}
	if level == 2 && req.To != Warehouse {
		return opError(CodeRecallForbidden, opTransfer, "二级召回：调拨只允许调入药库")
	}
	if l.stock[req.From] < req.Quantity {
		return opError(CodeInsufficientStock, opTransfer, "出发位置库存不足")
	}
	l.move(req.From, req.To, req.Quantity)
	s.lastNow = req.Now
	return nil
}

// Dispense 发放：从某位置向某患者发出某批次数量。
func (s *System) Dispense(req DispenseReq) error {
	if !validID(req.DrugID) || !validID(req.BatchID) ||
		!validID(req.Location) || !validID(req.Patient) || !validQty(req.Quantity) {
		return opError(CodeInvalidParam, opDispense, "标识须为非空串，数量须在 [1,10^6]")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opDispense, req.Now); err != nil {
		return err
	}
	if !s.inv.hasBatch(req.DrugID, req.BatchID) {
		return opError(CodeNotFound, opDispense, "批次不存在")
	}
	level, _ := s.recs.effective(req.DrugID, req.BatchID)
	if level == 1 || level == 2 {
		return opError(CodeRecallForbidden, opDispense,
			levelName(level)+"召回：禁止发放")
	}
	if level == 3 && !req.Consent {
		return opError(CodeConsentRequired, opDispense, "三级召回：发放须带知情确认")
	}
	l := s.inv.ledger(req.DrugID, req.BatchID)
	if l.stock[req.Location] < req.Quantity {
		return opError(CodeInsufficientStock, opDispense, "该位置库存不足")
	}
	l.dispense(req.Now, req.Location, req.Patient, req.Quantity)
	s.lastNow = req.Now
	return nil
}

// Return 退药：患者退回某批次数量，药品只进入药库。任何等级下都允许。
func (s *System) Return(req ReturnReq) error {
	if !validID(req.DrugID) || !validID(req.BatchID) ||
		!validID(req.Patient) || !validQty(req.Quantity) {
		return opError(CodeInvalidParam, opReturn, "标识须为非空串，数量须在 [1,10^6]")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opReturn, req.Now); err != nil {
		return err
	}
	if !s.inv.hasBatch(req.DrugID, req.BatchID) {
		return opError(CodeNotFound, opReturn, "批次不存在")
	}
	l := s.inv.ledger(req.DrugID, req.BatchID)
	if l.heldQty(req.Patient) < req.Quantity {
		return opError(CodeReturnExceeded, opReturn, "退药数量超过该患者该批次尚未退回的总量")
	}
	l.acceptReturn(req.Patient, req.Quantity)
	s.lastNow = req.Now
	return nil
}

// RegisterRecall 登记召回。登记后立即生效，覆盖此后入库的批次。
func (s *System) RegisterRecall(req RegisterRecallReq) error {
	if !validID(req.RecallID) || !validID(req.DrugID) ||
		!validID(req.LotLow) || !validID(req.LotHigh) ||
		req.LotLow > req.LotHigh ||
		req.Level < 1 || req.Level > maxLevel {
		return opError(CodeInvalidParam, opRegister,
			"标识/批号端点须为非空串，端点须 low<=high，等级须在 [1,3]")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opRegister, req.Now); err != nil {
		return err
	}
	if req.IssueAt > req.Now {
		return opError(CodeInvalidParam, opRegister, "问题始发时刻不得晚于 now")
	}
	if s.recs.hasID(req.RecallID) {
		return opError(CodeInvalidState, opRegister, "召回编号重复")
	}
	s.recs.register(&recallEntry{
		id:      req.RecallID,
		drugID:  req.DrugID,
		lotLow:  req.LotLow,
		lotHigh: req.LotHigh,
		level:   req.Level,
		issueAt: req.IssueAt,
	})
	s.lastNow = req.Now
	return nil
}

// ReleaseRecall 解除召回，只撤销该条登记。
func (s *System) ReleaseRecall(req ReleaseRecallReq) error {
	if !validID(req.RecallID) {
		return opError(CodeInvalidParam, opRelease, "召回编号须为非空串")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opRelease, req.Now); err != nil {
		return err
	}
	r, ok := s.recs.get(req.RecallID)
	if !ok {
		return opError(CodeNotFound, opRelease, "召回不存在")
	}
	if !r.active {
		return opError(CodeInvalidState, opRelease, "召回已解除")
	}
	s.recs.release(req.RecallID)
	s.lastNow = req.Now
	return nil
}

// QueryBatch 给出某批次各位置库存、有效等级与并列最严的召回编号集合。
func (s *System) QueryBatch(req BatchQueryReq) (*BatchInfo, error) {
	if !validID(req.DrugID) || !validID(req.BatchID) {
		return nil, opError(CodeInvalidParam, opQuery, "药品/批号须为非空串")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opQuery, req.Now); err != nil {
		return nil, err
	}
	if !s.inv.hasBatch(req.DrugID, req.BatchID) {
		return nil, opError(CodeNotFound, opQuery, "批次不存在")
	}
	level, ids := s.recs.effective(req.DrugID, req.BatchID)
	if ids == nil {
		ids = []string{}
	}
	l := s.inv.ledger(req.DrugID, req.BatchID)
	s.lastNow = req.Now
	return &BatchInfo{
		DrugID:          req.DrugID,
		BatchID:         req.BatchID,
		Stock:           l.stockSnapshot(),
		EffectiveLevel:  level,
		ActiveRecallIDs: ids,
	}, nil
}

// RecoveryList 针对某条召生成追回清单。
func (s *System) RecoveryList(req RecoveryReq) (*RecoveryResult, error) {
	if !validID(req.RecallID) {
		return nil, opError(CodeInvalidParam, opRecovery, "召回编号须为非空串")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opRecovery, req.Now); err != nil {
		return nil, err
	}
	r, ok := s.recs.get(req.RecallID)
	if !ok {
		return nil, opError(CodeNotFound, opRecovery, "召回不存在")
	}
	// 已解除召回清单不可查；三级召回不允许查清单。
	if !r.active {
		return nil, opError(CodeInvalidState, opRecovery, "召回已解除，清单不可查")
	}
	if r.level == 3 {
		return nil, opError(CodeInvalidState, opRecovery, "三级召回不可查询追回清单")
	}
	// 只遍历该召回药品的批次与该批次的发放记录：
	// 与其他药品的发放记录总数无关。
	type pb struct{ patient, batch string }
	qty := map[pb]int{}
	for _, l := range s.inv.drugBatches(r.drugID) {
		if !r.covers(l.batchID) {
			continue
		}
		for patient, left := range l.outstandingSince(r.issueAt) {
			qty[pb{patient, l.batchID}] += left
		}
	}
	keys := make([]pb, 0, len(qty))
	for k, v := range qty {
		if v > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].batch != keys[j].batch {
			return keys[i].batch < keys[j].batch
		}
		return keys[i].patient < keys[j].patient
	})
	items := make([]RecoveryItem, 0, len(keys))
	for _, k := range keys {
		items = append(items, RecoveryItem{
			Patient: k.patient, BatchID: k.batch, DrugID: r.drugID, Qty: qty[k],
		})
	}
	s.lastNow = req.Now
	return &RecoveryResult{RecallID: req.RecallID, Items: items}, nil
}

func levelName(level int) string {
	switch level {
	case 1:
		return "一级"
	case 2:
		return "二级"
	default:
		return "三级"
	}
}

// LastNow 返回当前单调时钟（测试与文档化使用）。
func (s *System) LastNow() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastNow
}
