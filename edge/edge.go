// Package edge 实现缓存条目新鲜度与预热队列。
//
// Cache 不自持锁：每个操作都在 quota.Meter 的锁临界区内执行，
// 与 purge.Store 一起构成可串行化的控制面。
package edge

import (
	"errors"

	"ontology/purge"
	"ontology/quota"
)

var (
	// ErrInvalidArgument 表示参数非法（now 越界、路径非法、批大小或
	// budget 越界、Lmax 越界）。
	ErrInvalidArgument = errors.New("edge: invalid argument")
	// ErrQueueFull 表示入队后全局预热队列长度将超过 Lmax。
	ErrQueueFull = errors.New("edge: prewarm queue full")
)

const (
	// MaxPrewarmItems 是单批预热 URL 数的上限。
	MaxPrewarmItems = 100
	// MaxTickBudget 是单次 Tick 预算的上限。
	MaxTickBudget = 10_000
	// MaxQueueLen 是 Lmax 的上限。
	MaxQueueLen = 1_000_000
)

// State 是 Fresh 的查询结果；条目不存在与条目过期可区分。
type State int

const (
	Missing State = iota // 条目不存在
	Stale                // 条目存在但已过期
	Fresh                // 条目存在且新鲜
)

func (s State) String() string {
	switch s {
	case Fresh:
		return "Fresh"
	case Stale:
		return "Stale"
	default:
		return "Missing"
	}
}

// TickResult 是 Tick 的执行清单，两个清单各自按出队次序排列。
type TickResult struct {
	Filled  []string // 执行了 Fill 的 URL
	Skipped []string // 已新鲜而跳过的 URL
}

type queueItem struct {
	tenant string
	url    string
}

// Cache 保存各租户的缓存条目（filled 纪元）与全局预热队列。
type Cache struct {
	meter   *quota.Meter
	store   *purge.Store
	lmax    int
	entries map[string]map[string]uint64 // tenant -> url -> filled 纪元
	queue   []queueItem
	inQueue map[queueItem]struct{}
}

// NewCache 返回一个缓存控制面，lmax 为全局预热队列长度上限（1 到 10^6）。
func NewCache(m *quota.Meter, s *purge.Store, lmax int) (*Cache, error) {
	if lmax < 1 || lmax > MaxQueueLen {
		return nil, ErrInvalidArgument
	}
	return &Cache{
		meter:   m,
		store:   s,
		lmax:    lmax,
		entries: make(map[string]map[string]uint64),
		inQueue: make(map[queueItem]struct{}),
	}, nil
}

// QueueLen 返回当前全局预热队列长度。
func (c *Cache) QueueLen() int {
	c.meter.Lock()
	defer c.meter.Unlock()
	return len(c.queue)
}

// fillLocked 写入或覆盖条目，filled 记为当前纪元。
func (c *Cache) fillLocked(tenant, url string) {
	m := c.entries[tenant]
	if m == nil {
		m = make(map[string]uint64)
		c.entries[tenant] = m
	}
	m[url] = c.store.Epoch()
}

func (c *Cache) freshLocked(tenant, url string) State {
	filled, ok := c.entries[tenant][url]
	if !ok {
		return Missing
	}
	if filled >= c.store.MatchEpoch(tenant, url) {
		return Fresh
	}
	return Stale
}

// Fill 写入或覆盖缓存条目，filled 记为当前纪元。
// 拒绝次序：参数非法 > 时钟回退 > 租户不存在。
func (c *Cache) Fill(now int64, tenant, url string) error {
	if !quota.ValidNow(now) || !purge.ValidPath(url) || purge.IsDir(url) {
		return ErrInvalidArgument
	}
	c.meter.Lock()
	defer c.meter.Unlock()
	if err := c.meter.Touch(now, tenant); err != nil {
		return err
	}
	c.fillLocked(tenant, url)
	return nil
}

// Fresh 是只读查询：条目存在，且 filled 不小于所有匹配它的规则的纪元，
// 才为新鲜；filled 恰等于规则纪元也算新鲜。
func (c *Cache) Fresh(tenant, url string) State {
	c.meter.Lock()
	defer c.meter.Unlock()
	return c.freshLocked(tenant, url)
}

// Prewarm 把 URL 加入全局预热队列并计费，返回实际入队条数。
// 批内重复只算一次；已在队列中的同租户同 URL 不重复入队也不计费；
// 当前已新鲜的 URL 照常入队并计费。全有或全无。
// 拒绝次序：参数非法 > 时钟回退 > 租户不存在 > 预热配额不足 > 队列已满。
func (c *Cache) Prewarm(now int64, tenant string, urls []string) (int, error) {
	if !quota.ValidNow(now) || len(urls) == 0 || len(urls) > MaxPrewarmItems {
		return 0, ErrInvalidArgument
	}
	for _, u := range urls {
		if !purge.ValidPath(u) || purge.IsDir(u) {
			return 0, ErrInvalidArgument
		}
	}
	c.meter.Lock()
	defer c.meter.Unlock()
	seen := make(map[string]struct{}, len(urls))
	var todo []string
	for _, u := range urls {
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		if _, ok := c.inQueue[queueItem{tenant, u}]; ok {
			continue
		}
		todo = append(todo, u)
	}
	w := uint64(len(todo))
	if err := c.meter.CheckPrewarm(now, tenant, w); err != nil {
		return 0, err
	}
	if len(c.queue)+len(todo) > c.lmax {
		return 0, ErrQueueFull
	}
	c.meter.CommitPrewarm(now, tenant, w)
	for _, u := range todo {
		it := queueItem{tenant, u}
		c.queue = append(c.queue, it)
		c.inQueue[it] = struct{}{}
	}
	return len(todo), nil
}

// Tick 按先进先出从队首取至多 budget 项：执行时已新鲜则跳过并记入
// Skipped，否则执行 Fill 并记入 Filled；两种情况都出队且都不退配额。
// 拒绝次序：参数非法 > 时钟回退。
func (c *Cache) Tick(now int64, budget int) (TickResult, error) {
	var res TickResult
	if !quota.ValidNow(now) || budget < 1 || budget > MaxTickBudget {
		return res, ErrInvalidArgument
	}
	c.meter.Lock()
	defer c.meter.Unlock()
	if err := c.meter.AdvanceClock(now); err != nil {
		return res, err
	}
	n := budget
	if n > len(c.queue) {
		n = len(c.queue)
	}
	items := make([]queueItem, n)
	copy(items, c.queue[:n])
	c.queue = c.queue[n:]
	for _, it := range items {
		delete(c.inQueue, it)
		if c.freshLocked(it.tenant, it.url) == Fresh {
			res.Skipped = append(res.Skipped, it.url)
		} else {
			c.fillLocked(it.tenant, it.url)
			res.Filled = append(res.Filled, it.url)
		}
	}
	return res, nil
}
