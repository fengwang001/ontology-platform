package pivas

func (s *System) AcceptOrder(now int, in OrderInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1) 参数非法优先于时钟回退之外的一切业务判定。
	if in.ID == "" || in.Solvent == "" || len(in.Drugs) < 1 || len(in.Drugs) > 6 {
		return errf(ErrInvalidParam, "order id/solvent non-empty and 1..6 drugs")
	}
	seenDrug := make(map[string]struct{}, len(in.Drugs))
	for _, d := range in.Drugs {
		if d == "" {
			return errf(ErrInvalidParam, "drug id must be non-empty")
		}
		if _, dup := seenDrug[d]; dup {
			return errf(ErrInvalidParam, "duplicated drug %q in order", d)
		}
		seenDrug[d] = struct{}{}
	}

	// 2) 时钟回退：拒绝时不改任何状态与时钟。
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, exists := s.orders[in.ID]; exists {
		return errf(ErrInvalidParam, "order %q already accepted", in.ID)
	}

	// 3) 目录静态校验（药品不存在 > 禁忌 > 溶媒不兼容），并快照派生属性。
	o, err := s.validateOrder(in)
	if err != nil {
		return err
	}
	o.acceptedAt = now

	// 4) 枚举可行安排：存放方式 × 台 × （既有批次 / 新开批次）。
	var best *candidate
	for _, benchID := range s.benchIDs {
		bs := s.benches[benchID]
		active := bs.activeBatches(now)
		for seq, b := range active {
			if b.solvent != o.Solvent || b.cover != o.cover {
				continue
			}
			if len(b.orders) >= bs.bench.Capacity {
				continue
			}
			c, ok := s.simulateExisting(now, bs, b, o, seq)
			if !ok {
				continue
			}
			c.seq = bs.head + seq
			if best == nil || lessCandidate(c, *best) {
				cc := c
				best = &cc
			}
		}

		dur, hasDur := s.durationOK(1)
		if hasDur {
			start := s.earliestGapStart(now, bs, dur)
			ready := start + dur
			st, deliver, feasible := s.bestStorage(o, ready)
			if feasible {
				c := candidate{
					deliver: deliver, benchID: benchID, existing: false,
					seq: 1 << 30, storage: st, ready: ready, newStart: start,
				}
				if best == nil || lessCandidate(c, *best) {
					cc := c
					best = &cc
				}
			}
		}
	}

	if best == nil {
		// 5) 无可行安排时，再区分是否仅被外袋形态卡住：避光冲突优先于无可行安排。
		if o.cover {
			if err := s.checkLightConflict(now, o); err != nil {
				return err
			}
		}
		return errf(ErrNoFeasiblePlan, "order %q cannot be scheduled", o.ID)
	}

	// 6) 提交：所有模拟均为只读，此处才第一次改状态。
	s.commit(best, o)
	s.commitClock(now)
	return nil
}

// checkLightConflict 仅在“唯一阻碍是既有同溶媒批次外袋形态不符”时报避光冲突。
// 新开批次永远可以选用正确外袋，故通常不会出现该错误；该规则让错误类别可精确复现。
func (s *System) checkLightConflict(now int, o *Order) error {
	for _, benchID := range s.benchIDs {
		bs := s.benches[benchID]
		for _, b := range bs.activeBatches(now) {
			if b.solvent != o.Solvent || b.cover == o.cover {
				continue
			}
			if b.start < now || len(b.orders) >= bs.bench.Capacity {
				continue
			}
			if s.batchCouldOtherwiseFit(bs, b, o, now) {
				return errf(ErrLightConflict,
					"order %q needs cover=%v but batch %s uses cover=%v",
					o.ID, o.cover, b.id, b.cover)
			}
		}
	}
	return nil
}

// batchCouldOtherwiseFit 忽略外袋形态后粗判一个既有批次是否本可接纳该医嘱。
func (s *System) batchCouldOtherwiseFit(bs *benchState, b *batch, o *Order, now int) bool {
	dur, ok := s.durationOK(len(b.orders) + 1)
	if !ok {
		return false
	}
	end := b.start + dur
	bs.sorted()
	idx := indexOf(bs.batches, b)
	if next := nextBatch(bs.batches, idx); next != nil && end+bs.bench.ClearGap > next.start {
		return false
	}
	_, _, feasible := s.bestStorage(o, end)
	return feasible && s.allOrdersFeasible(b, end, nil, 0)
}

func (s *System) commit(c *candidate, o *Order) {
	o.storage = c.storage
	s.orders[o.ID] = o
	if c.b != nil {
		b := c.b
		b.orders = append(b.orders, o)
		o.batchID = b.id
		newDur := s.durations[len(b.orders)]
		if o.Urgent {
			b.dur = newDur
			s.propagateShifts(b)
			return
		}
		b.dur = newDur
		return
	}

	bs := s.benches[c.benchID]
	s.nextBatchSeq++
	b := &batch{
		id:      batchID(s.nextBatchSeq),
		benchID: c.benchID,
		solvent: o.Solvent,
		cover:   o.cover,
		start:   c.newStart,
		dur:     s.durations[1],
		orders:  []*Order{o},
	}
	o.batchID = b.id
	bs.batches = append(bs.batches, b)
	s.batchByID[b.id] = b
	s.batchBench[b.id] = c.benchID
	bs.sorted()
}

// propagateShifts 将紧急加入后该台后续批次的连锁顺延落地。
func (s *System) propagateShifts(target *batch) {
	bs := s.benches[target.benchID]
	gap := bs.bench.ClearGap
	bs.sorted()
	idx := indexOf(bs.batches, target)
	prevEnd := target.end()
	for k := idx + 1; k < len(bs.batches); k++ {
		cur := bs.batches[k]
		if need := prevEnd + gap; need > cur.start {
			cur.start = need
		}
		prevEnd = cur.end()
	}
}

func batchID(seq int) string {
	return "B" + itoa(seq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
