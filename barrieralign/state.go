package barrieralign

// chState 是单个输入通道的可变状态。
type chState struct {
	// nextBarrier 是该通道下一道必须收到的屏障编号（首道为 1）。
	nextBarrier int64
	// blocked 表示该通道是否已收到当前编号屏障、正处于对齐阻塞中。
	blocked bool
	// buffered 是该通道当前滞留在 queue 中的记录条数（不含屏障）。
	buffered int
}

// item 是全局 FIFO 队列中的一项：被阻塞通道的记录或下一号屏障。
// 两个通道共用一条队列，因此缓冲项严格按跨通道到达顺序排列。
type item struct {
	channel int
	key     string
	value   []byte
	barrier int64 // 记录为 0；屏障项为其编号
}

func (it item) isBarrier() bool { return it.barrier != 0 }

// core 是可整体复制的内部状态。批处理在副本上推演，全部成功后再整体提交，
// 从而保证被拒绝的批不会污染累加状态、缓冲、快照或输出流。
type core struct {
	ch     [2]chState
	counts map[string]int
	queue  []item
	out    []OutputEvent
	snaps  []Snapshot
}

func newCore() *core {
	return &core{
		ch: [2]chState{
			{nextBarrier: 1},
			{nextBarrier: 1},
		},
		counts: make(map[string]int),
	}
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp
}

// clone 生成深拷贝，使推演副本与已提交状态互不共享可变内存。
func (c *core) clone() *core {
	cp := &core{ch: c.ch}
	cp.counts = make(map[string]int, len(c.counts))
	for k, v := range c.counts {
		cp.counts[k] = v
	}
	cp.queue = make([]item, len(c.queue))
	for i, q := range c.queue {
		q.value = cloneBytes(q.value)
		cp.queue[i] = q
	}
	cp.out = make([]OutputEvent, len(c.out))
	for i, o := range c.out {
		o.Value = cloneBytes(o.Value)
		cp.out[i] = o
	}
	cp.snaps = make([]Snapshot, len(c.snaps))
	for i, s := range c.snaps {
		m := make(map[string]int, len(s.Counts))
		for k, v := range s.Counts {
			m[k] = v
		}
		cp.snaps[i] = Snapshot{Barrier: s.Barrier, Counts: m}
	}
	return cp
}
