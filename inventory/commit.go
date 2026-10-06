package inventory

import "container/heap"

// OrderLine 是一条订单行：商品与数量。
type OrderLine struct {
	Product string
	Qty     int64
}

// CommitRequest 描述一次订单承诺。
type CommitRequest struct {
	OrderID       string
	Lines         []OrderLine
	CommitTime    int64 // 承诺时刻，不得早于当前时刻
	AllowSplit    bool
	Duration      int64 // 预留时长，到期时刻 = 承诺时刻 + 预留时长
	MaxWarehouses int   // 整单涉及仓库数上限
}

// Allocation 表示某订单行从某仓库取用的数量。
type Allocation struct {
	Warehouse string
	Qty       int64
}

// LineAllocation 是一条订单行的发货仓分配结果。
type LineAllocation struct {
	Product string
	Qty     int64
	Splits  []Allocation
}

// CommitResult 是承诺成功后的结果：各行的发货仓分配与统一到期时刻。
type CommitResult struct {
	OrderID string
	Expiry  int64
	Lines   []LineAllocation
}

// Commit 执行订单承诺，整单全有或全无。
// 拒绝优先级：参数非法 > 时钟回退 > 订单重复 > 永久缺货 > 暂时缺货 > 拆分过多。
// 被拒绝时不改变任何状态与时钟（懒清理不改变任何可观察状态）。
func (s *System) Commit(req CommitRequest) (*CommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if err := validateCommit(req); err != nil {
		return nil, err
	}
	// 2. 时钟回退
	if err := s.checkClock(req.CommitTime); err != nil {
		return nil, err
	}
	now := req.CommitTime
	// 3. 订单重复：同一订单号存在仍有效的预留
	if e, ok := s.orders[req.OrderID]; ok && now < e.expiry {
		return nil, newError(CodeOrderDuplicate, "订单 %s 已存在有效预留", req.OrderID)
	}

	// 对涉及的所有桶做一次懒清理（以承诺时刻判定预留有效性）。
	products := make(map[string]bool)
	for _, l := range req.Lines {
		products[l.Product] = true
	}
	for _, w := range s.sorted {
		for p := range products {
			if b := w.buckets[p]; b != nil {
				b.cleanExpired(now, &s.examined)
			}
		}
	}

	// 4/5. 逐行分配；后面的行看到前面行已占用后的可承诺量。
	allocated := make(map[*bucket]int64)
	used := make(map[string]bool)
	lineResults := make([]LineAllocation, 0, len(req.Lines))
	for i, line := range req.Lines {
		la, ok := s.allocateLine(line, now, allocated, req.AllowSplit)
		if !ok {
			// 缺货分类：所有仓库现货加全部计划入库（忽略预留与到货时刻）
			// 不足为永久缺货，否则为暂时缺货；行下标即第一个不满足的行。
			var total int64
			for _, w := range s.sorted {
				if b := w.buckets[line.Product]; b != nil {
					total += b.onHand + b.inboundTotal
				}
			}
			code := CodeTemporaryShortage
			if total < line.Qty {
				code = CodePermanentShortage
			}
			return nil, shortageError(code, i, line.Product)
		}
		for _, a := range la.Splits {
			used[a.Warehouse] = true
		}
		lineResults = append(lineResults, la)
	}

	// 6. 拆分过多：整单涉及的不同仓库数超过上限即拒绝，
	// 不为凑够上限而改用其他取用方案。
	if len(used) > req.MaxWarehouses {
		return nil, newError(CodeTooManySplits,
			"订单涉及 %d 个仓库，超过上限 %d", len(used), req.MaxWarehouses)
	}

	// 承诺成功：各用到的仓库为该订单建立预留。
	expiry := now + req.Duration
	entry := &orderEntry{expiry: expiry}
	for _, la := range lineResults {
		for _, a := range la.Splits {
			b := s.bucketForWrite(a.Warehouse, la.Product)
			r := &Reservation{
				OrderID:   req.OrderID,
				Warehouse: a.Warehouse,
				Product:   la.Product,
				Qty:       a.Qty,
				Expiry:    expiry,
				alive:     true,
			}
			b.reserved += a.Qty
			heap.Push(&b.res, r)
			entry.recs = append(entry.recs, r)
		}
	}
	s.orders[req.OrderID] = entry
	s.clock = now
	return &CommitResult{OrderID: req.OrderID, Expiry: expiry, Lines: lineResults}, nil
}

func validateCommit(req CommitRequest) *Error {
	if req.OrderID == "" || req.CommitTime < 0 || req.Duration < 0 || len(req.Lines) == 0 {
		return newError(CodeInvalidParam, "参数非法：订单号为空、时刻/时长为负或订单行为空")
	}
	if req.MaxWarehouses < 1 {
		return newError(CodeInvalidParam, "参数非法：仓库数上限 %d 小于 1", req.MaxWarehouses)
	}
	for i, l := range req.Lines {
		if l.Product == "" || l.Qty <= 0 {
			return newError(CodeInvalidParam, "参数非法：订单行 %d 商品为空或数量非正", i)
		}
	}
	return nil
}

// allocateLine 为单个订单行选择发货仓。
// 不允许拆分时选可承诺量足够且优先序号最小的仓库全量满足；
// 允许拆分时按优先序号由小到大依次尽量取用。
func (s *System) allocateLine(line OrderLine, now int64, allocated map[*bucket]int64, allowSplit bool) (LineAllocation, bool) {
	la := LineAllocation{Product: line.Product, Qty: line.Qty}
	avail := func(w *warehouse) int64 {
		b := w.buckets[line.Product]
		if b == nil {
			return 0
		}
		return b.atp(now) - allocated[b]
	}
	if !allowSplit {
		for _, w := range s.sorted {
			if avail(w) >= line.Qty {
				b := s.bucketForWrite(w.id, line.Product)
				allocated[b] += line.Qty
				la.Splits = []Allocation{{Warehouse: w.id, Qty: line.Qty}}
				return la, true
			}
		}
		return la, false
	}
	remaining := line.Qty
	for _, w := range s.sorted {
		if remaining == 0 {
			break
		}
		take := avail(w)
		if take > remaining {
			take = remaining
		}
		if take > 0 {
			b := s.bucketForWrite(w.id, line.Product)
			allocated[b] += take
			la.Splits = append(la.Splits, Allocation{Warehouse: w.id, Qty: take})
			remaining -= take
		}
	}
	if remaining > 0 {
		return la, false
	}
	return la, true
}

// ConfirmOutbound 确认出库：把订单仍有效的预留转为现货扣减。
// 订单不存在报 CodeOrderNotFound；预留已到期报 CodeReservationExpired。
func (s *System) ConfirmOutbound(orderID string, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" || t < 0 {
		return newError(CodeInvalidParam, "参数非法")
	}
	if err := s.checkClock(t); err != nil {
		return err
	}
	e, ok := s.orders[orderID]
	if !ok {
		return newError(CodeOrderNotFound, "订单 %s 不存在", orderID)
	}
	if t >= e.expiry {
		return newError(CodeReservationExpired, "订单 %s 的预留已过期", orderID)
	}

	// 汇总各桶需扣减的数量。
	need := make(map[*bucket]int64)
	for _, r := range e.recs {
		need[s.bucketForWrite(r.Warehouse, r.Product)] += r.Qty
	}
	// 第一阶段：懒清理过期预留，并把已到期的计划入库转为现货（只转换一次），
	// 然后校验现货充足；任何修改都在校验通过后才发生。
	for b, q := range need {
		b.cleanExpired(t, &s.examined)
		if b.onHand+b.inboundArrivedBy(t) < q {
			return newError(CodeInsufficientOnHand, "现货不足，无法确认出库")
		}
	}
	for b := range need {
		b.materialize(t)
	}
	// 第二阶段：扣减现货并终止预留。
	for b, q := range need {
		b.onHand -= q
	}
	for _, r := range e.recs {
		if r.alive {
			r.alive = false
			s.bucketOf(r.Warehouse, r.Product).reserved -= r.Qty
		}
	}
	delete(s.orders, orderID)
	s.clock = t
	return nil
}

// Release 释放订单仍有效的预留。
// 对已到期或不存在的订单报 CodeOrderNotFound（与订单号重复可区分）。
func (s *System) Release(orderID string, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" || t < 0 {
		return newError(CodeInvalidParam, "参数非法")
	}
	if err := s.checkClock(t); err != nil {
		return err
	}
	e, ok := s.orders[orderID]
	if !ok || t >= e.expiry {
		return newError(CodeOrderNotFound, "订单 %s 不存在或预留已到期", orderID)
	}
	for _, r := range e.recs {
		if r.alive {
			r.alive = false
			s.bucketOf(r.Warehouse, r.Product).reserved -= r.Qty
		}
	}
	delete(s.orders, orderID)
	s.clock = t
	return nil
}
