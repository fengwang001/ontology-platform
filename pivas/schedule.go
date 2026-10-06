package pivas

import "sort"

// stableFor 返回某医嘱在指定存放方式下的整袋稳定秒数：
// 同一存放方式下取各药品稳定秒数的最小者。
func (o *Order) stableFor(st Storage) int {
	if st == Cold {
		return o.coldStable
	}
	return o.roomStable
}

// validAt 判断医嘱在配置完成时刻 ready、以存放方式 st 送达时，
// 是否同时满足有效期与按时两项约束。
// 恰到期时刻视为已失效，因此要求严格小于 expireAt；
// 送达恰等于要求时刻视为按时，因此允许等于 dueAt。
func (s *System) validAt(o *Order, ready int, st Storage) bool {
	transport, ok := s.transport[st]
	if !ok {
		return false
	}
	deliver := ready + transport
	expire := ready + o.stableFor(st)
	return deliver < expire && deliver <= o.DueAt
}

func (b *batch) end() int { return b.start + b.dur }

func (bs *benchState) sorted() {
	sort.SliceStable(bs.batches, func(i, j int) bool {
		if bs.batches[i].start != bs.batches[j].start {
			return bs.batches[i].start < bs.batches[j].start
		}
		return bs.batches[i].id < bs.batches[j].id
	})
}

// advanceHead 用二分把 head 快进到第一个未结束（end > now）的批次。
// 历史前缀的跳过是 O(log N)，不随历史批次数线性增长。
func (bs *benchState) advanceHead(now int) {
	lo, hi := bs.head, len(bs.batches)
	for lo < hi {
		mid := (lo + hi) / 2
		if bs.batches[mid].end() <= now {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	bs.head = lo
}

// activeBatches 返回当前未开始批次（不含历史前缀）。
func (bs *benchState) activeBatches(now int) []*batch {
	bs.advanceHead(now)
	return bs.batches[bs.head:]
}

// validateOrder 做受理前的静态校验，错误严格按优先级返回。
// 调用方须持锁。成功时返回按受理时目录快照派生的 Order（尚未编组）。
func (s *System) validateOrder(in OrderInput) (*Order, error) {
	if in.ID == "" || in.Solvent == "" || len(in.Drugs) < 1 || len(in.Drugs) > 6 {
		return nil, errf(ErrInvalidParam, "order id/solvent non-empty and 1..6 drugs")
	}
	seen := make(map[string]struct{}, len(in.Drugs))
	for _, d := range in.Drugs {
		if d == "" {
			return nil, errf(ErrInvalidParam, "drug id must be non-empty")
		}
		if _, dup := seen[d]; dup {
			return nil, errf(ErrInvalidParam, "duplicated drug %q in order", d)
		}
		seen[d] = struct{}{}
	}
	if _, exists := s.orders[in.ID]; exists {
		return nil, errf(ErrInvalidParam, "order %q already accepted", in.ID)
	}

	for _, d := range in.Drugs {
		if _, ok := s.catalog.lookup(d); !ok {
			return nil, errf(ErrDrugNotFound, "drug %q not in catalog", d)
		}
	}

	room, cold := 0, 0
	for i, d := range in.Drugs {
		info, _ := s.catalog.lookup(d)
		if i == 0 || info.RoomStableSec < room {
			room = info.RoomStableSec
		}
		if i == 0 || info.ColdStableSec < cold {
			cold = info.ColdStableSec
		}
	}

	// 禁忌配对：哈希查找 O(k^2)，与目录禁忌总数无关。
	for i := 0; i < len(in.Drugs); i++ {
		for j := i + 1; j < len(in.Drugs); j++ {
			if s.catalog.isBadAt(pairKeyOf(in.Drugs[i], in.Drugs[j]), s.catalog.version) {
				return nil, errf(ErrIncompatiblePair, "drugs %q and %q are incompatible",
					in.Drugs[i], in.Drugs[j])
			}
		}
	}

	cover := false
	for _, d := range in.Drugs {
		info, _ := s.catalog.lookup(d)
		if info.SolventClass != in.Solvent {
			return nil, errf(ErrSolventMismatch, "drug %q uses solvent %q, order uses %q",
				d, info.SolventClass, in.Solvent)
		}
		if info.LightSensitive {
			cover = true
		}
	}

	o := &Order{
		ID:         in.ID,
		Drugs:      append([]string(nil), in.Drugs...),
		Solvent:    in.Solvent,
		DueAt:      in.DueAt,
		Urgent:     in.Urgent,
		roomStable: room,
		coldStable: cold,
		cover:      cover,
		catVersion: s.catalog.version,
	}
	return o, nil
}

// allOrdersFeasible 校验批次内全部医嘱在新的完成时刻下仍满足约束。
func (s *System) allOrdersFeasible(b *batch, ready int, extra *Order, extraStorage Storage) bool {
	for _, o := range b.orders {
		if !s.validAt(o, ready, o.storage) {
			return false
		}
	}
	if extra != nil && !s.validAt(extra, ready, extraStorage) {
		return false
	}
	return true
}
