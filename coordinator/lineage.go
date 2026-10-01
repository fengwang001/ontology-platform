package coordinator

import "fmt"

// AdvanceClock 把时钟推进到 t。t 必须不小于当前时钟，否则整体拒绝。
func (c *Coordinator) AdvanceClock(t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < c.clock {
		return &RejectError{
			Op:     "AdvanceClock",
			Reason: ReasonClockRegress,
			Detail: fmt.Sprintf("clock %d -> %d regresses", c.clock, t),
		}
	}
	c.clock = t
	return nil
}

// Append 向键 key 所属的开放分片追加一条记录，返回（分片 ID, 位置）。
// 位置在该分片内从 0 起单调递增，不丢不重。
func (c *Coordinator) Append(key int) (int, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key < 0 || key >= c.k {
		return 0, 0, &RejectError{
			Op:     "Append",
			Reason: ReasonKeyOutOfRange,
			Detail: fmt.Sprintf("key %d not in [0,%d)", key, c.k),
		}
	}
	s := c.routeLocked(key)
	pos := s.Appended
	s.Appended++
	return s.ID, pos, nil
}

// routeLocked 在开放分片中查找覆盖 key 的分片（二分，区间升序不重叠）。
func (c *Coordinator) routeLocked(key int) *Shard {
	lo, hi := 0, len(c.openList)
	for lo < hi {
		mid := (lo + hi) / 2
		s := c.shards[c.openList[mid]]
		switch {
		case key < s.Lo:
			hi = mid
		case key >= s.Hi:
			lo = mid + 1
		default:
			return s
		}
	}
	return nil // 不会发生：开放分片覆盖整个键空间
}

// closeLocked 关闭开放分片：记录结束位置，并从开放列表移除。
func (c *Coordinator) closeLocked(s *Shard) {
	s.Open = false
	s.End = s.Appended
	for i, id := range c.openList {
		if id == s.ID {
			c.openList = append(c.openList[:i], c.openList[i+1:]...)
			return
		}
	}
}

// insertOpenLocked 按区间升序把开放分片插入开放列表。
func (c *Coordinator) insertOpenLocked(s *Shard) {
	i := 0
	for i < len(c.openList) && c.shards[c.openList[i]].Lo < s.Lo {
		i++
	}
	c.openList = append(c.openList, 0)
	copy(c.openList[i+1:], c.openList[i:])
	c.openList[i] = s.ID
}

// addChildLocked 创建子分片并登记到开放列表。
func (c *Coordinator) addChildLocked(lo, hi int, parents []int) *Shard {
	id := c.nextID
	c.nextID++
	child := &Shard{
		ID:      id,
		Lo:      lo,
		Hi:      hi,
		Parents: append([]int(nil), parents...),
		Open:    true,
		End:     -1,
	}
	c.shards[id] = child
	c.insertOpenLocked(child)
	return child
}

// Split 把开放分片 id 在 mid 处分裂为 [lo,mid) 与 [mid,hi) 两个子分片。
// 原分片关闭，结束位置为已追加条数；子分片位置从 0 起，以原分片为父。
// 依次校验：分片不存在、已关闭、分裂点越界；拒绝时不改变任何状态。
func (c *Coordinator) Split(id int, mid int) (int, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		return 0, 0, &RejectError{Op: "Split", Reason: ReasonShardNotFound,
			Detail: fmt.Sprintf("shard %d does not exist", id)}
	}
	if !s.Open {
		return 0, 0, &RejectError{Op: "Split", Reason: ReasonShardClosed,
			Detail: fmt.Sprintf("shard %d already closed", id)}
	}
	if mid <= s.Lo || mid >= s.Hi {
		return 0, 0, &RejectError{Op: "Split", Reason: ReasonSplitPointInvalid,
			Detail: fmt.Sprintf("mid %d not in (%d,%d)", mid, s.Lo, s.Hi)}
	}
	c.closeLocked(s)
	left := c.addChildLocked(s.Lo, mid, []int{s.ID})
	right := c.addChildLocked(mid, s.Hi, []int{s.ID})
	return left.ID, right.ID, nil
}

// Merge 把两个首尾相接的开放分片合并为一个并集子分片（与参数次序无关）。
// 两个原分片关闭，结束位置为各自已追加条数；子分片位置从 0 起，以它们为父。
// 依次校验：分片不存在、已关闭、不相邻（含两参数相同）；拒绝时不改变任何状态。
func (c *Coordinator) Merge(a, b int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sa, ok := c.shards[a]
	if !ok {
		return 0, &RejectError{Op: "Merge", Reason: ReasonShardNotFound,
			Detail: fmt.Sprintf("shard %d does not exist", a)}
	}
	sb, ok := c.shards[b]
	if !ok {
		return 0, &RejectError{Op: "Merge", Reason: ReasonShardNotFound,
			Detail: fmt.Sprintf("shard %d does not exist", b)}
	}
	if !sa.Open {
		return 0, &RejectError{Op: "Merge", Reason: ReasonShardClosed,
			Detail: fmt.Sprintf("shard %d already closed", a)}
	}
	if !sb.Open {
		return 0, &RejectError{Op: "Merge", Reason: ReasonShardClosed,
			Detail: fmt.Sprintf("shard %d already closed", b)}
	}
	lo, hi := sa, sb
	if hi.Lo < lo.Lo {
		lo, hi = hi, lo
	}
	if a == b || lo.Hi != hi.Lo {
		return 0, &RejectError{Op: "Merge", Reason: ReasonNotAdjacent,
			Detail: fmt.Sprintf("shards %d[%d,%d) and %d[%d,%d) not adjacent",
				sa.ID, sa.Lo, sa.Hi, sb.ID, sb.Lo, sb.Hi)}
	}
	c.closeLocked(sa)
	c.closeLocked(sb)
	child := c.addChildLocked(lo.Lo, hi.Hi, []int{sa.ID, sb.ID})
	return child.ID, nil
}
