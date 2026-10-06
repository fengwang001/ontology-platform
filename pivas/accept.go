package pivas

// candidate 是一种可行安排。
type candidate struct {
	deliver  int     // 送达时刻（首选键）
	benchID  string  // 台编号（次选键）
	existing bool    // 已存在批次优先于新开批次
	seq      int     // 同档并列时的稳定次序
	storage  Storage // 该医嘱的存放方式
	ready    int     // 配置完成时刻
	b        *batch  // 非空=加入既有批次；nil=新开批次
	newStart int     // b==nil 时的新批次开始时刻
}

// bestStorage 在给定完成时刻下为新医嘱选择送达最早的可行存放方式。
// 并列时室温优先（送达时刻完全相同，纯属确定性取舍，文档说明）。
func (s *System) bestStorage(o *Order, ready int) (Storage, int, bool) {
	bestSt := Room
	bestDeliver := 0
	found := false
	for _, st := range []Storage{Room, Cold} {
		if s.validAt(o, ready, st) {
			d := ready + s.transport[st]
			if !found || d < bestDeliver {
				found, bestSt, bestDeliver = true, st, d
			}
		}
	}
	return bestSt, bestDeliver, found
}

// simulateExisting 评估把 o 加入既有批次 b。
// 普通医嘱不允许改变任何后续批次开始时刻；紧急医嘱允许连锁顺延。
func (s *System) simulateExisting(now int, bs *benchState, b *batch, o *Order, activeIdx int) (candidate, bool) {
	if b.start < now {
		return candidate{}, false
	}
	n := len(b.orders)
	dur, ok := s.durationOK(n + 1)
	if !ok {
		return candidate{}, false
	}
	gap := bs.bench.ClearGap

	bs.sorted()
	bs.advanceHead(now)
	list := bs.batches
	idx := bs.head + activeIdx
	newEnd := b.start + dur

	if !o.Urgent {
		if next := nextBatch(list, idx); next != nil && newEnd+gap > next.start {
			return candidate{}, false
		}
		st, deliver, ok := s.bestStorage(o, newEnd)
		if !ok || !s.allOrdersFeasible(b, newEnd, o, st) {
			return candidate{}, false
		}
		return candidate{
			deliver: deliver, benchID: bs.bench.ID, existing: true,
			storage: st, ready: newEnd, b: b,
		}, true
	}

	// 紧急：连锁顺延。
	// newStarts 保存每个受影响批次的试探开始时刻，绝不写回批次对象。
	newStarts := make(map[*batch]int, len(list)-idx)
	newStarts[b] = b.start
	prevEnd := newEnd
	for k := idx + 1; k < len(list); k++ {
		cur := list[k]
		need := prevEnd + gap
		st := cur.start
		if need > st {
			if st < now {
				return candidate{}, false // 已开始批次不可顺延
			}
			st = need
		}
		newStarts[cur] = st
		prevEnd = st + cur.dur
	}
	st, deliver, ok := s.bestStorage(o, newEnd)
	if !ok {
		return candidate{}, false
	}
	for bb, st0 := range newStarts {
		ready := st0 + bb.dur
		if bb == b {
			ready = newEnd
		}
		if !s.allOrdersFeasible(bb, ready, nil, 0) {
			return candidate{}, false
		}
	}
	if !s.validAt(o, newEnd, st) {
		return candidate{}, false
	}
	c := candidate{
		deliver: deliver, benchID: bs.bench.ID, existing: true,
		storage: st, ready: newEnd, b: b,
	}
	return c, true
}

// earliestGapStart 计算新开批次在台时间线上最早可行的开始时刻。
// 台时间线在不变量 b[i+1].start >= b[i].end()+gap 下总可插入；
// 该函数只负责在既有间隙中找最早位置，开销与未开始批次数同阶，
// 已开始的历史前缀可由调用方跳过。
func (s *System) earliestGapStart(now int, bs *benchState, dur int) int {
	gap := bs.bench.ClearGap
	bs.sorted()
	bs.advanceHead(now)
	list := bs.batches
	start := now
	for i := bs.head; i < len(list); i++ {
		cur := list[i]
		if cur.end() <= now {
			continue
		}
		if start+dur+gap <= cur.start {
			return start
		}
		start = cur.end() + gap
	}
	return start
}

func indexOf(list []*batch, b *batch) int {
	for i, x := range list {
		if x == b {
			return i
		}
	}
	return -1
}

func nextBatch(list []*batch, idx int) *batch {
	if idx+1 < len(list) {
		return list[idx+1]
	}
	return nil
}

func lessCandidate(a, c candidate) bool {
	if a.deliver != c.deliver {
		return a.deliver < c.deliver
	}
	if a.benchID != c.benchID {
		return a.benchID < c.benchID
	}
	if a.existing != c.existing {
		return a.existing && !c.existing // 已存在批次优先于新开批次
	}
	return a.seq < c.seq
}
