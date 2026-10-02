package swap

// Alloc 分配一个空闲槽位，新槽位 count 为 0、cache 为真，返回槽位号。
// 全部可用槽位都在用时返回 ErrNoSpace 且不改任何状态。
func (l *Ledger) Alloc() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.probes = 0
	if l.used == l.n-1 {
		return -1, ErrNoSpace
	}
	s := -1
	// 第 1 步：cur 簇内编号不小于 cursor 的最小空闲槽位。
	if l.cur >= 0 {
		s = l.scanFree(l.cur, l.cursor, true)
		if s >= 0 {
			l.cursor = s + 1
		}
	}
	if s < 0 {
		// 第 2 步：旧 cur 整簇空闲则入队，取队首簇为 cur。
		if l.cur >= 0 && l.clusterFree[l.cur] == l.clusterUsable[l.cur] {
			l.pushQueue(l.cur)
		}
		l.cur = -1
		if l.qHead < len(l.queue) {
			c := l.popQueue()
			l.cur = c
			s = l.firstSlot(c)
			l.cursor = s + 1
		} else {
			// 第 3 步（回退）：经线段树取含空闲槽位的最小编号簇，
			// 即全局最小空闲槽位所在簇。
			l.probes++
			c := l.seg[1]
			if c == l.segInf {
				// 不可达：进入前已确认存在空闲槽位。
				return -1, ErrNoSpace
			}
			l.cur = c
			s = l.scanFree(c, 0, false)
			l.cursor = s + 1
		}
	}
	l.count[s] = 0
	l.cache[s] = true
	l.used++
	c := s / l.k
	l.clusterFree[c]--
	l.segUpdate(c)
	return s, nil
}

// checkRange 校验槽位编号在 1 到 N-1。
func (l *Ledger) checkRange(s int) error {
	if s < 1 || s > l.n-1 {
		return ErrRange
	}
	return nil
}

// dupOne 是 Dup 的无锁实现，调用方须持有 l.mu。
func (l *Ledger) dupOne(s int) error {
	if err := l.checkRange(s); err != nil {
		return err
	}
	if l.freeSlot(s) {
		return ErrNotInUse
	}
	if int(l.count[s]) >= l.maxCount {
		return ErrOverflow
	}
	l.count[s]++
	return nil
}

// freeOne 是 Free 的无锁实现，调用方须持有 l.mu。
func (l *Ledger) freeOne(s int) error {
	if err := l.checkRange(s); err != nil {
		return err
	}
	if l.freeSlot(s) {
		return ErrNotInUse
	}
	if l.count[s] == 0 {
		return ErrUnderflow
	}
	l.applyFree(s)
	return nil
}

// applyFree 执行一次已校验的 count 减一，必要时回收槽位。
func (l *Ledger) applyFree(s int) {
	l.count[s]--
	if l.count[s] == 0 && !l.cache[s] {
		l.makeFree(s)
	}
}

// cacheAddOne 是 CacheAdd 的无锁实现，调用方须持有 l.mu。
func (l *Ledger) cacheAddOne(s int) error {
	if err := l.checkRange(s); err != nil {
		return err
	}
	if l.freeSlot(s) {
		return ErrNotInUse
	}
	if l.cache[s] {
		return ErrExists
	}
	l.cache[s] = true
	return nil
}

// cacheDropOne 是 CacheDrop 的无锁实现，调用方须持有 l.mu。
func (l *Ledger) cacheDropOne(s int) error {
	if err := l.checkRange(s); err != nil {
		return err
	}
	if l.freeSlot(s) {
		return ErrNotInUse
	}
	if !l.cache[s] {
		return ErrNoCache
	}
	l.cache[s] = false
	if l.count[s] == 0 {
		l.makeFree(s)
	}
	return nil
}

// Dup 把槽位 s 的引用计数加一（拷贝换出项，例如 fork）。
func (l *Ledger) Dup(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dupOne(s)
}

// Free 把槽位 s 的引用计数减一。
func (l *Ledger) Free(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.freeOne(s)
}

// CacheAdd 把槽位 s 的交换缓存标志置为真。
func (l *Ledger) CacheAdd(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cacheAddOne(s)
}

// CacheDrop 把槽位 s 的交换缓存标志置为假。
func (l *Ledger) CacheDrop(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cacheDropOne(s)
}

// Fork 等价于按给定次序依次 Dup，但整批原子：先按次序在副本上推演，
// 遇到第一个失败的元素就返回（下标, 错误）且整批不生效；
// 同一槽位出现多次时按推演中的累计状态判定。成功时返回 (-1, nil)。
func (l *Ledger) Fork(slots []int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pending := make(map[int]int, len(slots))
	for i, s := range slots {
		if err := l.checkRange(s); err != nil {
			return i, err
		}
		c := int(l.count[s])
		if p, ok := pending[s]; ok {
			c = p
		}
		if c == 0 && !l.cache[s] {
			return i, ErrNotInUse
		}
		if c >= l.maxCount {
			return i, ErrOverflow
		}
		pending[s] = c + 1
	}
	for _, s := range slots {
		l.count[s]++
	}
	return -1, nil
}

// Release 等价于按给定次序依次 Free，但整批原子：先按次序在副本上推演，
// 遇到第一个失败的元素就返回（下标, 错误）且整批不生效；
// 同一槽位出现多次时按推演中的累计状态判定。成功时返回 (-1, nil)。
func (l *Ledger) Release(slots []int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pending := make(map[int]int, len(slots))
	for i, s := range slots {
		if err := l.checkRange(s); err != nil {
			return i, err
		}
		c := int(l.count[s])
		if p, ok := pending[s]; ok {
			c = p
		}
		if c == 0 && !l.cache[s] {
			return i, ErrNotInUse
		}
		if c == 0 {
			return i, ErrUnderflow
		}
		pending[s] = c - 1
	}
	for _, s := range slots {
		l.applyFree(s)
	}
	return -1, nil
}

// Info 返回槽位 s 的（count, cache）；编号越界时返回 (0, false)。
func (l *Ledger) Info(s int) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s < 1 || s > l.n-1 {
		return 0, false
	}
	return int(l.count[s]), l.cache[s]
}

// Used 返回在用槽位数。
func (l *Ledger) Used() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

// FreeSlots 返回空闲槽位数，恒有 Used()+FreeSlots() == N-1。
func (l *Ledger) FreeSlots() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n - 1 - l.used
}

// FreeClusters 返回整簇空闲队列的快照（队首在前）。
func (l *Ledger) FreeClusters() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]int, len(l.queue)-l.qHead)
	copy(out, l.queue[l.qHead:])
	return out
}

// Current 返回当前簇 cur 与游标 cursor；cur 为 -1 表示当前簇为空。
func (l *Ledger) Current() (cur, cursor int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur, l.cursor
}
