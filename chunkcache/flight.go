package chunkcache

import "context"

// flight 表示针对某对象键一个连续切片区间的在途回源。
//
// 同一时刻每个 (key,index) 最多属于一个 flight：
// 规划阶段在 mu 下扫描注册表，把缺失段中尚未在途的连续下标交给一个 leader
// 合并成单次 Origin.Fetch；已在途的下标成为该 flight 的 follower。
// 因此“相邻缺失切片合并回源、同一切片不重复回源”在并发下仍成立。
type flight struct {
	key    string
	first  int
	last   int
	expect string

	done   chan struct{}
	result FetchResult
	err    error

	// 发布期记账（仅 leader 在锁内填写）。
	recorder  *GetResponse
	malformed bool
	gen       int64 // 成功回源分配的代际号；0 表示尚未分配
}

// flightPlan 为一次规划产生的回源安排。
type flightPlan struct {
	groups []*flight // 按区间顺序排列；本请求是其中每个 flight 的 leader 或 follower
	leader []bool
}

// registerLocked 在 mu 下为本请求的缺失段 [segFirst,segLast] 登记/复用 flight。
// 返回的 groups 按段内顺序覆盖 segFirst..segLast，互不重叠且连续。
// 调用方持有 c.mu。
func (c *Cache) registerLocked(key, expect string, segFirst, segLast int, registry map[int]*flight) flightPlan {
	var plan flightPlan
	i := segFirst
	for i <= segLast {
		if f := registry[i]; f != nil {
			// 已在途：加入既有 flight，消费其覆盖到的下标。
			plan.groups = append(plan.groups, f)
			plan.leader = append(plan.leader, false)
			i = f.last + 1
			continue
		}
		// 找到从 i 开始、注册表中空闲的最长连续区间；其右端由
		// “下一个已在途下标”或“本缺失段末端”界定。
		j := i
		for j+1 <= segLast && registry[j+1] == nil {
			j++
		}
		f := &flight{
			key:    key,
			first:  i,
			last:   j,
			expect: expect,
			done:   make(chan struct{}),
		}
		for k := i; k <= j; k++ {
			registry[k] = f
		}
		plan.groups = append(plan.groups, f)
		plan.leader = append(plan.leader, true)
		i = j + 1
	}
	return plan
}

// publishFn 在锁内把成功回源结果提交进缓存（发现路径与数据路径不同）。
type publishFn func(res FetchResult)

// executeLeader 在锁外对源站发起一次合并回源；成功时在锁内调用 publish 提交，
// 然后摘除注册表并广播，保证 follower 醒来时 meta/切片均已可见。
func (c *Cache) executeLeader(ctx context.Context, f *flight, publish publishFn) {
	c.log.Logf("[origin] call key=%s idx=[%d,%d] expect=%q", f.key, f.first, f.last, f.expect)
	res, err := c.origin.Fetch(ctx, FetchRequest{
		Key:             f.key,
		First:           f.first,
		Last:            f.last,
		ExpectedVersion: f.expect,
	})
	c.mu.Lock()
	f.result, f.err = res, err
	for k := f.first; k <= f.last; k++ {
		if reg := c.flightRegs[f.key]; reg != nil && reg[k] == f {
			delete(reg, k)
		}
	}
	c.stats.OriginCalls++
	if err == nil {
		c.stats.OriginChunks += int64(len(res.Chunks))
		publish(res)
	}
	close(f.done)
	c.mu.Unlock()
}

// waitFlight 等待一个 flight 完成（锁外）。
func waitFlight(ctx context.Context, f *flight) (FetchResult, error) {
	select {
	case <-f.done:
		return f.result, f.err
	case <-ctx.Done():
		return FetchResult{}, ctx.Err()
	}
}
