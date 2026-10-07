package pivas

// orderRec 是已受理医嘱的内部记录，目录数据在受理时快照，
// 之后的目录变更不追溯。
type orderRec struct {
	id         string
	requiredAt int64
	urgent     bool

	roomStable int64 // 受理时各药品室温稳定秒数的最小者
	coldStable int64 // 受理时各药品冷藏稳定秒数的最小者
	solvent    string
	lightProof bool // 整袋是否使用避光外袋

	storage   Storage
	transport int64 // 受理时按存放方式确定的运送时长

	benchID   string
	batchID   string
	cancelled bool
	done      bool // 所在批次已完成
}

func (r *orderRec) stable() int64 {
	if r.storage == StorageCold {
		return r.coldStable
	}
	return r.roomStable
}

// batch 一个洁净台批次：同台、同溶媒、同外袋形态。
type batch struct {
	id         string
	solvent    string
	lightProof bool
	start      int64
	orders     []*orderRec
}

// bench 洁净台及其批次队列，队列按开始时刻升序，仅含未完成批次。
type bench struct {
	cfg   BenchConfig
	queue []*batch
	// histFreeAt 是已完成批次留下的占用下限：最后完成批次结束时刻 + 清场间隔。
	histFreeAt int64
	// freeAt 是该台下一批次最早可行开始时刻。
	freeAt int64
}

func (b *bench) dur(count int) int64 { return b.cfg.DurationByCount[count] }

func (b *bench) end(ba *batch) int64 { return ba.start + b.dur(len(ba.orders)) }

// recomputeFreeAt 由已完成历史下限与队尾占据重算最早可行时刻。
func (b *bench) recomputeFreeAt() {
	f := b.histFreeAt
	if n := len(b.queue); n > 0 {
		if t := b.end(b.queue[n-1]) + b.cfg.ClearanceSec; t > f {
			f = t
		}
	}
	b.freeAt = f
}

// purgeCompleted 移除已完成（结束时刻 <= now）的批次，并将其医嘱标记为已完成；
// 每个批次至多被移除一次，均摊 O(1)。
func (b *bench) purgeCompleted(now int64) {
	i := 0
	for i < len(b.queue) && b.end(b.queue[i]) <= now {
		if t := b.end(b.queue[i]) + b.cfg.ClearanceSec; t > b.histFreeAt {
			b.histFreeAt = t
		}
		for _, r := range b.queue[i].orders {
			r.done = true
		}
		i++
	}
	if i > 0 {
		b.queue = append([]*batch(nil), b.queue[i:]...)
		b.recomputeFreeAt()
	}
}
