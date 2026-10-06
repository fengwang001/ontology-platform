package pivas

func (s *System) CancelOrder(now int, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if orderID == "" {
		return errf(ErrInvalidParam, "order id must be non-empty")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return errf(ErrInvalidParam, "order %q not found", orderID)
	}
	b := s.findBatch(o)
	if b == nil {
		return errf(ErrBadState, "order %q has no batch", orderID)
	}
	bs := s.benches[b.benchID]
	if b.start < now {
		return errf(ErrBadState, "order %q batch already started", orderID)
	}

	// 从批次移除；批次开始时刻不因取消而提前。
	kept := b.orders[:0]
	for _, x := range b.orders {
		if x != o {
			kept = append(kept, x)
		}
	}
	b.orders = kept
	delete(s.orders, orderID)

	if len(b.orders) == 0 {
		// 空批次删除：后续批次的开始时刻保持不变（不提前）。
		list := bs.batches
		for i, x := range list {
			if x == b {
				bs.batches = append(list[:i], list[i+1:]...)
				break
			}
		}
		delete(s.batchByID, b.id)
		delete(s.batchBench, b.id)
	} else {
		b.dur = s.durations[len(b.orders)]
	}
	s.commitClock(now)
	return nil
}

// findBatch 通过全局批次索引 O(1) 定位批次。
func (s *System) findBatch(o *Order) *batch {
	return s.batchByID[o.batchID]
}

func (s *System) QueryOrder(now int, orderID string) (*OrderInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" {
		return nil, errf(ErrInvalidParam, "order id must be non-empty")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return nil, errf(ErrInvalidParam, "order %q not found", orderID)
	}
	b := s.findBatch(o)
	ready := b.end()
	st := o.storage
	deliver := ready + s.transport[st]
	expire := ready + o.stableFor(st)
	return &OrderInfo{
		OrderID:    o.ID,
		Storage:    st,
		BenchID:    b.benchID,
		BatchID:    b.id,
		BatchStart: b.start,
		ReadyAt:    ready,
		ExpireAt:   expire,
		DeliverAt:  deliver,
		OnTime:     deliver < expire && deliver <= o.DueAt,
		Covered:    o.cover,
	}, nil
}
