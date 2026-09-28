package replication

import (
	"fmt"
	"sync"
)

// Cluster 是副本注册表，并承载领导者恢复截断与复制流程。
type Cluster struct {
	mu       sync.RWMutex
	replicas map[string]*Replica
	// Logf 非空时打印恢复每一步的输入、截断点与判定依据，供测试与调试使用。
	Logf func(format string, args ...any)
}

func NewCluster() *Cluster {
	return &Cluster{replicas: make(map[string]*Replica)}
}

// AddReplica 注册副本；空 id 或重复注册返回 ErrInvalidArgument。
func (c *Cluster) AddReplica(r *Replica) error {
	if r == nil || r.id == "" {
		return fmt.Errorf("add replica: %w: empty id", ErrInvalidArgument)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.replicas[r.id]; dup {
		return fmt.Errorf("add replica %q: %w: duplicate id", r.id, ErrInvalidArgument)
	}
	c.replicas[r.id] = r
	return nil
}

func (c *Cluster) replica(id string) (*Replica, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty replica id", ErrInvalidArgument)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.replicas[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownReplica, id)
	}
	return r, nil
}

func (c *Cluster) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// GenerationBounds 是领导者对世代查询的响应：返回世代 gen 在领导者
// 日志中的 [start, end) 区间。世代 0 为非法参数；领导者从未见过的世代
// 返回 ErrUnknownGeneration。
func (c *Cluster) GenerationBounds(leaderID string, gen uint64) (start, end uint64, err error) {
	if gen == 0 {
		return 0, 0, fmt.Errorf("%w: generation 0", ErrInvalidArgument)
	}
	leader, err := c.replica(leaderID)
	if err != nil {
		return 0, 0, err
	}
	leader.mu.RLock()
	defer leader.mu.RUnlock()
	start, end, ok := leader.generationBoundsLocked(gen)
	if !ok {
		return 0, 0, fmt.Errorf("%w: leader %q has no generation %d", ErrUnknownGeneration, leaderID, gen)
	}
	return start, end, nil
}

// boundsIn 在快照上计算世代 gen 的 [start, end) 区间。
func boundsIn(cache []GenerationStart, logEnd uint64, gen uint64) (start, end uint64, ok bool) {
	for i, gs := range cache {
		if gs.Generation == gen {
			start = gs.StartOffset
			if i+1 < len(cache) {
				end = cache[i+1].StartOffset
			} else {
				end = logEnd
			}
			return start, end, true
		}
	}
	return 0, 0, false
}

// trimCache 删除起始位点不小于 point 的缓存项。
func trimCache(cache []GenerationStart, point uint64) []GenerationStart {
	keep := 0
	for _, gs := range cache {
		if gs.StartOffset < point {
			cache[keep] = gs
			keep++
		}
	}
	return cache[:keep]
}

// planTruncation 在快照上纯函数式地推演多轮截断，返回截断点。
// 不做任何修改；推演失败（如分叉超出缓存可证明范围）返回错误。
func (c *Cluster) planTruncation(fEnd uint64, fCache []GenerationStart, lCache []GenerationStart, lEnd uint64) (uint64, error) {
	point := fEnd
	cache := append([]GenerationStart(nil), fCache...)
	for round := 1; point > 0; round++ {
		if len(cache) == 0 {
			return 0, fmt.Errorf("%w: follower has %d entries without generation cache", ErrLogDiverged, point)
		}
		gen := cache[len(cache)-1].Generation
		fStart := cache[len(cache)-1].StartOffset
		lStart, lGenEnd, ok := boundsIn(lCache, lEnd, gen)
		switch {
		case !ok:
			// 领导者从未拥有该世代：整个世代都是分叉残留，整代截掉。
			c.logf("round %d: gen=%d not on leader -> truncate whole generation to %d", round, gen, fStart)
			point = fStart
			cache = cache[:len(cache)-1]
		case fStart != lStart:
			// 同一世代在两侧起始位点不同，说明更早的日志已经分叉，
			// 缓存无法证明公共前缀，整体拒绝。
			return 0, fmt.Errorf("%w: generation %d starts at %d on follower but %d on leader",
				ErrLogDiverged, gen, fStart, lStart)
		case point > lGenEnd:
			// 跟随者在该世代内比领导者多写了未提交条目，截到领导者该世代末端。
			c.logf("round %d: gen=%d follower end %d > leader end %d -> truncate to %d", round, gen, point, lGenEnd, lGenEnd)
			point = lGenEnd
			cache = trimCache(cache, point)
		default:
			c.logf("round %d: gen=%d follower end %d within leader [%d,%d) -> common prefix, stop at %d",
				round, gen, point, lStart, lGenEnd, point)
			return point, nil
		}
	}
	if point == 0 && fEnd > 0 {
		c.logf("all %d entries belong to generations unknown to leader -> truncate to 0", fEnd)
	}
	return point, nil
}

// Recover 把 follower 的分叉尾部截断到与 leader 的最长公共前缀。
// 只截断不拉取；追平由 Replicate 完成。返回截断点与是否发生了截断。
// 任何校验失败都整体拒绝，不改动任何副本的日志或世代缓存。
func (c *Cluster) Recover(leaderID, followerID string) (point uint64, changed bool, err error) {
	if leaderID == followerID {
		return 0, false, fmt.Errorf("%w: leader and follower are the same replica %q", ErrInvalidArgument, leaderID)
	}
	leader, err := c.replica(leaderID)
	if err != nil {
		return 0, false, err
	}
	follower, err := c.replica(followerID)
	if err != nil {
		return 0, false, err
	}
	for {
		leader.mu.RLock()
		lCache := append([]GenerationStart(nil), leader.cache...)
		lEnd := uint64(len(leader.entries))
		leader.mu.RUnlock()

		follower.mu.RLock()
		fCache := append([]GenerationStart(nil), follower.cache...)
		fEnd := uint64(len(follower.entries))
		follower.mu.RUnlock()

		point, err = c.planTruncation(fEnd, fCache, lCache, lEnd)
		if err != nil {
			return 0, false, err
		}
		if point == fEnd {
			c.logf("recover %s from %s: nothing to truncate, end=%d", followerID, leaderID, fEnd)
			return point, false, nil
		}

		// 应用阶段：快照与规划之间若跟随者被并发改动则重试，
		// 保证截断严格基于已验证的推演结果。
		follower.mu.Lock()
		if uint64(len(follower.entries)) != fEnd || !cacheEqual(follower.cache, fCache) {
			follower.mu.Unlock()
			continue
		}
		c.logf("recover %s from %s: truncate [%d,%d) -> end=%d", followerID, leaderID, point, fEnd, point)
		follower.truncateLocked(point)
		follower.mu.Unlock()
		return point, true, nil
	}
}

func cacheEqual(a, b []GenerationStart) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Replicate 把 leader 在 follower 末端之后的条目复制给 follower（追平）。
// 复制前逐条校验公共前缀一致；不一致说明存在未恢复的分叉，整体拒绝。
func (c *Cluster) Replicate(leaderID, followerID string) (int, error) {
	if leaderID == followerID {
		return 0, fmt.Errorf("%w: leader and follower are the same replica %q", ErrInvalidArgument, leaderID)
	}
	leader, err := c.replica(leaderID)
	if err != nil {
		return 0, err
	}
	follower, err := c.replica(followerID)
	if err != nil {
		return 0, err
	}
	leader.mu.RLock()
	lEntries := append([]Entry(nil), leader.entries...)
	leader.mu.RUnlock()

	follower.mu.Lock()
	defer follower.mu.Unlock()
	fEnd := uint64(len(follower.entries))
	if fEnd > uint64(len(lEntries)) {
		return 0, fmt.Errorf("%w: follower %q end %d ahead of leader %q end %d; run Recover first",
			ErrLogDiverged, followerID, fEnd, leaderID, len(lEntries))
	}
	for i, e := range follower.entries {
		if e != lEntries[i] {
			return 0, fmt.Errorf("%w: follower %q mismatches leader %q at offset %d; run Recover first",
				ErrLogDiverged, followerID, leaderID, i)
		}
	}
	fresh := lEntries[fEnd:]
	if err := follower.appendLocked(fresh); err != nil {
		return 0, fmt.Errorf("replicate to %q: %w", followerID, err)
	}
	c.logf("replicate %s -> %s: appended %d entries, end=%d", leaderID, followerID, len(fresh), len(follower.entries))
	return len(fresh), nil
}
