package lineage

import "sort"
import "sync"

// Logger 用于记录每次操作的输入、输出与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// Coordinator 协调分片谱系、追加记录与消费租约。
// 所有方法可被并发调用；内部以互斥锁串行化状态变更。
type Coordinator struct {
	mu        sync.Mutex
	k         int
	ttl       int64
	maxLeases int
	clock     Clock
	nextID    ShardID
	shards    map[ShardID]*shard
	open      []ShardID // 当前构成键空间划分的开放分片，按 lo 升序
	logger    Logger
}

// New 创建协调器。K 为键空间大小（键 [0,K)），ttl 为租约有效期。
func New(K int, ttl int64, maxLeasesPerWorker int, logger Logger) *Coordinator {
	root := &shard{id: 0, lo: 0, hi: K}
	return &Coordinator{
		k:         K,
		ttl:       ttl,
		maxLeases: maxLeasesPerWorker,
		nextID:    1,
		shards:    map[ShardID]*shard{0: root},
		open:      []ShardID{0},
		logger:    logger,
	}
}

func (c *Coordinator) logf(format string, args ...any) {
	if c.logger != nil {
		c.logger.Printf(format, args...)
	}
}

// routeOpen 返回当前唯一覆盖 key 的开放分片。调用方持锁。
func (c *Coordinator) routeOpen(key int) *shard {
	for _, id := range c.open {
		s := c.shards[id]
		if key >= s.lo && key < s.hi {
			return s
		}
	}
	return nil
}

// replaceOpen 将 open 中的 oldID 替换为 newIDs（保持按 lo 升序）。调用方持锁。
func (c *Coordinator) replaceOpen(oldID ShardID, newIDs ...ShardID) {
	out := make([]ShardID, 0, len(c.open)-1+len(newIDs))
	for _, id := range c.open {
		if id == oldID {
			out = append(out, newIDs...)
		} else {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return c.shards[out[i]].lo < c.shards[out[j]].lo
	})
	c.open = out
}

// AdvanceClock 将时钟单调推进到 now。
func (c *Coordinator) AdvanceClock(now Clock) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.clock {
		c.logf("AdvanceClock(%d) -> %v (判定: 当前时钟 %d，不允许回退)", now, ErrClockBackward, c.clock)
		return ErrClockBackward
	}
	c.clock = now
	c.logf("AdvanceClock(%d) -> ok (判定: 时钟推进到 %d)", now, now)
	return nil
}

// Append 将键 key 的一条记录追加到当前覆盖它的唯一开放分片。
func (c *Coordinator) Append(key int) (AppendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key < 0 || key >= c.k {
		c.logf("Append(key=%d) -> %v (判定: 键空间为 [0,%d))", key, ErrKeyOutOfRange, c.k)
		return AppendResult{}, ErrKeyOutOfRange
	}
	s := c.routeOpen(key)
	pos := s.appended
	s.appended++
	c.logf("Append(key=%d) -> {shard:%d pos:%d} (判定: 路由到开放分片 [%d,%d)，该分片第 %d 条)",
		key, s.id, pos, s.lo, s.hi, pos)
	return AppendResult{Shard: s.id, Position: pos}, nil
}

// Split 在 mid 处将开放分片 shard 分裂为 [lo,mid) 与 [mid,hi)。
func (c *Coordinator) Split(id ShardID, mid int) (left ShardID, right ShardID, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		c.logf("Split(id=%d,mid=%d) -> %v (判定: 分片不存在)", id, mid, ErrShardNotFound)
		return 0, 0, ErrShardNotFound
	}
	if s.closed {
		c.logf("Split(id=%d,mid=%d) -> %v (判定: 分片已关闭)", id, mid, ErrShardClosed)
		return 0, 0, ErrShardClosed
	}
	if mid <= s.lo || mid >= s.hi {
		c.logf("Split(id=%d,mid=%d) -> %v (判定: 需满足 %d < mid < %d)", id, mid, ErrSplitPointOutOfRange, s.lo, s.hi)
		return 0, 0, ErrSplitPointOutOfRange
	}
	s.closed = true
	leftID, rightID := c.nextID, c.nextID+1
	c.nextID += 2
	c.shards[leftID] = &shard{id: leftID, lo: s.lo, hi: mid, parents: []ShardID{s.id}}
	c.shards[rightID] = &shard{id: rightID, lo: mid, hi: s.hi, parents: []ShardID{s.id}}
	c.replaceOpen(s.id, leftID, rightID)
	c.logf("Split(id=%d,mid=%d) -> {%d,%d} (判定: 父分片 [%d,%d) 关闭，结束位置=%d；左子 [%d,%d)、右子 [%d,%d) 位置从 0 起)",
		id, mid, leftID, rightID, s.lo, s.hi, s.appended, s.lo, mid, mid, s.hi)
	return leftID, rightID, nil
}

// Merge 将两个首尾相接的开放分片合并为一个并集分片（与参数次序无关）。
func (c *Coordinator) Merge(a, b ShardID) (ShardID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sa, oka := c.shards[a]
	sb, okb := c.shards[b]
	if !oka || !okb {
		c.logf("Merge(a=%d,b=%d) -> %v (判定: 分片不存在, a存在=%v b存在=%v)", a, b, ErrShardNotFound, oka, okb)
		return 0, ErrShardNotFound
	}
	if sa.closed || sb.closed {
		c.logf("Merge(a=%d,b=%d) -> %v (判定: 存在已关闭分片, a关闭=%v b关闭=%v)", a, b, ErrShardClosed, sa.closed, sb.closed)
		return 0, ErrShardClosed
	}
	// 判定首尾相接（参数次序无关）；相同分片不相邻。
	var lo, mid, hi int
	switch {
	case a != b && sa.hi == sb.lo:
		lo, mid, hi = sa.lo, sa.hi, sb.hi
	case a != b && sb.hi == sa.lo:
		lo, mid, hi = sb.lo, sb.hi, sa.hi
	default:
		c.logf("Merge(a=%d,b=%d) -> %v (判定: [%d,%d) 与 [%d,%d) 不首尾相接或为同一分片)",
			a, b, ErrShardsNotAdjacent, sa.lo, sa.hi, sb.lo, sb.hi)
		return 0, ErrShardsNotAdjacent
	}
	sa.closed = true
	sb.closed = true
	childID := c.nextID
	c.nextID++
	parents := []ShardID{a, b}
	sort.Slice(parents, func(i, j int) bool { return parents[i] < parents[j] })
	c.shards[childID] = &shard{id: childID, lo: lo, hi: hi, parents: parents}
	c.replaceOpen(a, childID)
	c.replaceOpen(b)
	c.logf("Merge(a=%d,b=%d) -> %d (判定: 父分片 [%d,%d) 与 [%d,%d) 在 %d 处相接并关闭，结束位置分别为 %d、%d；并集子分片 [%d,%d) 位置从 0 起)",
		a, b, childID, sa.lo, sa.hi, sb.lo, sb.hi, mid, sa.appended, sb.appended, lo, hi)
	return childID, nil
}

// Grant 由 worker 领取分片 id 的消费租约。
func (c *Coordinator) Grant(id ShardID, worker WorkerID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		c.logf("Grant(shard=%d,worker=%q) -> %v (判定: 分片不存在)", id, worker, ErrShardNotFound)
		return ErrShardNotFound
	}
	if s.drained() {
		c.logf("Grant(shard=%d,worker=%q) -> %v (判定: 分片已排空 closed=%v committed=%d end=%d)",
			id, worker, ErrShardDrained, s.closed, s.committed, s.appended)
		return ErrShardDrained
	}
	var undrained []ShardID
	for _, pid := range s.parents {
		if p := c.shards[pid]; !p.drained() {
			undrained = append(undrained, pid)
		}
	}
	if len(undrained) > 0 {
		err := &GrantError{Err: ErrParentsUndrained, UndrainedParents: undrained}
		c.logf("Grant(shard=%d,worker=%q) -> %v (判定: 父分片 %v 未全部排空)", id, worker, err, s.parents)
		return err
	}
	if s.leaseValid(c.clock) {
		c.logf("Grant(shard=%d,worker=%q) -> %v (判定: 当前有效持有者=%q，到期时钟=%d，现时钟=%d)",
			id, worker, ErrAlreadyHeld, s.holder, s.expires, c.clock)
		return ErrAlreadyHeld
	}
	count := 0
	for _, other := range c.shards {
		if other.holder == worker && other.leaseValid(c.clock) {
			count++
		}
	}
	if count >= c.maxLeases {
		c.logf("Grant(shard=%d,worker=%q) -> %v (判定: 该工作者有效租约数=%d 已达上限=%d)",
			id, worker, ErrWorkerLeaseLimit, count, c.maxLeases)
		return ErrWorkerLeaseLimit
	}
	s.holder = worker
	s.expires = c.clock + Clock(c.ttl)
	c.logf("Grant(shard=%d,worker=%q) -> ok (判定: 授予成功，生效时钟=%d，到期时钟=%d)",
		id, worker, c.clock, s.expires)
	return nil
}

// Renew 由有效持有者续租。
func (c *Coordinator) Renew(id ShardID, worker WorkerID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok || !s.leaseValid(c.clock) || s.holder != worker {
		c.logf("Renew(shard=%d,worker=%q) -> %v (判定: 非有效持有者, 存在=%v 现时钟=%d)",
			id, worker, ErrNotValidHolder, ok, c.clock)
		return ErrNotValidHolder
	}
	s.expires = c.clock + Clock(c.ttl)
	c.logf("Renew(shard=%d,worker=%q) -> ok (判定: 续租成功，新到期时钟=%d)", id, worker, s.expires)
	return nil
}

// Commit 由有效持有者提交进度 progress。
func (c *Coordinator) Commit(id ShardID, worker WorkerID, progress int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok || !s.leaseValid(c.clock) || s.holder != worker {
		c.logf("Commit(shard=%d,worker=%q,progress=%d) -> %v (判定: 非有效持有者, 存在=%v 现时钟=%d)",
			id, worker, progress, ErrNotValidHolder, ok, c.clock)
		return ErrNotValidHolder
	}
	if progress < s.committed {
		c.logf("Commit(shard=%d,worker=%q,progress=%d) -> %v (判定: 进度回退, 当前已提交=%d)",
			id, worker, progress, ErrProgressBackward, s.committed)
		return ErrProgressBackward
	}
	if progress > s.appended {
		c.logf("Commit(shard=%d,worker=%q,progress=%d) -> %v (判定: 超过已追加条数=%d)",
			id, worker, progress, ErrProgressBeyondAppend, s.appended)
		return ErrProgressBeyondAppend
	}
	s.committed = progress
	c.logf("Commit(shard=%d,worker=%q,progress=%d) -> ok (判定: 提交成功, 已追加=%d, 已排空=%v)",
		id, worker, progress, s.appended, s.drained())
	return nil
}

// Get 返回单个分片的快照。
func (c *Coordinator) Get(id ShardID) (ShardInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.shards[id]
	if !ok {
		return ShardInfo{}, false
	}
	return c.snapshot(s), true
}

// List 返回全部分片快照（按 ID 升序）。
func (c *Coordinator) List() []ShardInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]ShardID, 0, len(c.shards))
	for id := range c.shards {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]ShardInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.snapshot(c.shards[id]))
	}
	return out
}

func (c *Coordinator) snapshot(s *shard) ShardInfo {
	return ShardInfo{
		ID:          s.id,
		Lo:          s.lo,
		Hi:          s.hi,
		Closed:      s.closed,
		EndPosition: s.appended,
		Appended:    s.appended,
		Committed:   s.committed,
		Drained:     s.drained(),
		Parents:     append([]ShardID(nil), s.parents...),
		Holder:      s.holder,
		LeaseValid:  s.leaseValid(c.clock),
		ExpiresAt:   s.expires,
	}
}
