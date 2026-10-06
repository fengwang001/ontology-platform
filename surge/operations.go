package surge

// operations.go 实现骑手/订单操作、派单结算与评估入口。
// 统一约定：参数校验（空 ID）在加锁前；加锁后先校验时钟，
// 再按“对象不存在 → 状态错误 → 资格错误 → 评估过频”的次序判定，
// 任一步失败都在修改状态前返回，因此被拒操作不留任何痕迹。

func (s *System) RiderOnline(riderID, areaID string, at int64) *Error {
	if riderID == "" || areaID == "" {
		return errf(KindInvalidParam, "empty id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return e
	}
	r := s.ledger.riders[riderID]
	if r == nil {
		return errf(KindRiderNotFound, "rider not found: %s", riderID)
	}
	a := s.ledger.areas[areaID]
	if a == nil {
		return errf(KindAreaNotFound, "area not found: %s", areaID)
	}
	if r.online {
		return errf(KindRiderAlreadyOnline, "rider already online: %s", riderID)
	}
	r.online = true
	r.area = areaID
	r.enteredAt = at
	if r.held < s.cfg.MaxHeldOrders {
		a.available++
	}
	s.clock = at
	return nil
}

func (s *System) RiderOffline(riderID string, at int64) *Error {
	if riderID == "" {
		return errf(KindInvalidParam, "empty rider id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return e
	}
	r := s.ledger.riders[riderID]
	if r == nil {
		return errf(KindRiderNotFound, "rider not found: %s", riderID)
	}
	if !r.online {
		return errf(KindRiderAlreadyOffline, "rider already offline: %s", riderID)
	}
	if r.held > 0 {
		return errf(KindRiderBusy, "rider holds %d orders", r.held)
	}
	if r.held < s.cfg.MaxHeldOrders {
		s.ledger.areas[r.area].available--
	}
	r.online = false
	r.area = ""
	s.clock = at
	return nil
}

func (s *System) RiderMove(riderID, toAreaID string, at int64) *Error {
	if riderID == "" || toAreaID == "" {
		return errf(KindInvalidParam, "empty id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return e
	}
	r := s.ledger.riders[riderID]
	if r == nil {
		return errf(KindRiderNotFound, "rider not found: %s", riderID)
	}
	if !r.online {
		return errf(KindRiderOffline, "rider offline: %s", riderID)
	}
	to := s.ledger.areas[toAreaID]
	if to == nil {
		return errf(KindAreaNotFound, "area not found: %s", toAreaID)
	}
	if r.area == toAreaID {
		return errf(KindNoNeedToMove, "rider already in area: %s", toAreaID)
	}
	from := s.ledger.areas[r.area]
	if r.held < s.cfg.MaxHeldOrders {
		from.available--
		to.available++
	}
	r.area = toAreaID
	r.enteredAt = at
	s.clock = at
	return nil
}

func (s *System) CreateOrder(orderID, areaID string, at int64) (int, *Error) {
	if orderID == "" || areaID == "" {
		return 0, errf(KindInvalidParam, "empty id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return 0, e
	}
	a := s.ledger.areas[areaID]
	if a == nil {
		return 0, errf(KindAreaNotFound, "area not found: %s", areaID)
	}
	if _, ok := s.ledger.orders[orderID]; ok {
		return 0, errf(KindInvalidParam, "order already exists: %s", orderID)
	}
	od := &order{
		id:         orderID,
		area:       areaID,
		createdAt:  at,
		state:      orderPending,
		lockedTier: a.currentTier,
	}
	s.ledger.orders[orderID] = od
	a.pending++
	s.clock = at
	return od.lockedTier, nil
}

func (s *System) CancelOrder(orderID string, at int64) *Error {
	if orderID == "" {
		return errf(KindInvalidParam, "empty order id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return e
	}
	od := s.ledger.orders[orderID]
	if od == nil {
		return errf(KindOrderNotFound, "order not found: %s", orderID)
	}
	switch od.state {
	case orderCancelled:
		return errf(KindOrderCancelled, "order cancelled: %s", orderID)
	case orderCompleted:
		return errf(KindOrderCompleted, "order completed: %s", orderID)
	case orderDispatched:
		return errf(KindOrderAlreadyDispatched, "order dispatched: %s", orderID)
	}
	od.state = orderCancelled
	s.ledger.areas[od.area].pending--
	s.clock = at
	return nil
}

func (s *System) DispatchOrder(orderID, riderID string, at int64) *Error {
	if orderID == "" || riderID == "" {
		return errf(KindInvalidParam, "empty id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return e
	}
	od := s.ledger.orders[orderID]
	if od == nil {
		return errf(KindOrderNotFound, "order not found: %s", orderID)
	}
	r := s.ledger.riders[riderID]
	if r == nil {
		return errf(KindRiderNotFound, "rider not found: %s", riderID)
	}
	switch od.state {
	case orderCancelled:
		return errf(KindOrderCancelled, "order cancelled: %s", orderID)
	case orderCompleted:
		return errf(KindOrderCompleted, "order completed: %s", orderID)
	case orderDispatched:
		return errf(KindOrderAlreadyDispatched, "order dispatched: %s", orderID)
	}
	// 资格判定次序：不在线 → 不在本区 → 持单已满。
	if !r.online {
		return errf(KindRiderOffline, "rider offline: %s", riderID)
	}
	if r.area != od.area {
		return errf(KindRiderWrongArea, "rider in %s, order in %s", r.area, od.area)
	}
	if r.held >= s.cfg.MaxHeldOrders {
		return errf(KindRiderAtCapacity, "rider at capacity: %s", riderID)
	}
	// 在派单时刻快照补贴资格：骑手进入本区时刻不晚于订单创建时刻。
	od.eligibleAtDispatch = r.online && r.area == od.area && r.enteredAt <= od.createdAt
	od.state = orderDispatched
	od.riderID = riderID
	r.held++
	a := s.ledger.areas[od.area]
	a.pending--
	if r.held == s.cfg.MaxHeldOrders {
		a.available-- // 恰好跨过上限边界
	}
	s.clock = at
	return nil
}

func (s *System) CompleteOrder(orderID string, at int64) (int64, bool, *Error) {
	if orderID == "" {
		return 0, false, errf(KindInvalidParam, "empty order id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return 0, false, e
	}
	od := s.ledger.orders[orderID]
	if od == nil {
		return 0, false, errf(KindOrderNotFound, "order not found: %s", orderID)
	}
	switch od.state {
	case orderCancelled:
		return 0, false, errf(KindOrderCancelled, "order cancelled: %s", orderID)
	case orderCompleted:
		return 0, false, errf(KindOrderCompleted, "order completed: %s", orderID)
	case orderPending:
		return 0, false, errf(KindOrderAlreadyDispatched, "order not dispatched: %s", orderID)
	}
	r := s.ledger.riders[od.riderID]
	wasAtCapacity := r.held == s.cfg.MaxHeldOrders
	r.held--
	// 恢复运力只取决于持单是否从上限降回上限以下，
	// 恢复点为骑手当前所在区域（携单移动后可能已不是订单区域）。
	if wasAtCapacity && r.online {
		s.ledger.areas[r.area].available++
	}
	od.state = orderCompleted

	amount := int64(0)
	waived := !od.eligibleAtDispatch
	if od.eligibleAtDispatch {
		amount = s.cfg.Subsidies[od.lockedTier]
	}
	entry := &SubsidyEntry{
		RiderID: od.riderID,
		OrderID: orderID,
		At:      at,
		Tier:    od.lockedTier,
		Amount:  amount,
		Waived:  waived,
	}
	s.ledger.entries[od.riderID] = append(s.ledger.entries[od.riderID], entry)
	od.settled = true
	s.clock = at
	return amount, waived, nil
}

func (s *System) Evaluate(areaID string, at int64) *Error {
	if areaID == "" {
		return errf(KindInvalidParam, "empty area id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkClock(at); e != nil {
		return e
	}
	a := s.ledger.areas[areaID]
	if a == nil {
		return errf(KindAreaNotFound, "area not found: %s", areaID)
	}
	if a.hasEval && at-a.lastEval < s.cfg.MinEvalInterval {
		return errf(KindEvaluationTooFrequent, "eval interval %d < %d", at-a.lastEval, s.cfg.MinEvalInterval)
	}
	target, inf := targetTier(s.cfg.Thresholds, a.pending, a.available)
	oldTier := a.currentTier
	newTier, newConfirms, changed := applyEvaluation(a.currentTier, a.downConfirms, target, s.cfg.DowngradeConfirms)
	a.currentTier = newTier
	a.downConfirms = newConfirms
	a.lastEval = at
	a.hasEval = true
	if changed {
		ratio := 0.0
		if a.available > 0 {
			ratio = float64(a.pending) / float64(a.available)
		}
		s.events[areaID] = append(s.events[areaID], &TierEvent{
			Area:     areaID,
			At:       at,
			FromTier: oldTier,
			ToTier:   newTier,
			Ratio:    ratio,
			RatioInf: inf,
		})
	}
	s.clock = at
	return nil
}
