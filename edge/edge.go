// Package edge 实现缓存条目新鲜度、刷新纪元与预热队列控制面。
package edge

import (
	"errors"
	"sync"

	"ontology/purge"
	"ontology/quota"
)

var (
	// ErrInvalidArg 参数非法。
	ErrInvalidArg = errors.New("edge: invalid argument")
	// ErrClockBack now 小于已接受操作的最大 now。
	ErrClockBack = errors.New("edge: clock moved backwards")
	// ErrUnknownTenant 租户不存在。
	ErrUnknownTenant = errors.New("edge: unknown tenant")
	// ErrURLQuota URL 刷新配额不足。
	ErrURLQuota = errors.New("edge: url purge quota exceeded")
	// ErrDirQuota 目录刷新配额不足。
	ErrDirQuota = errors.New("edge: dir purge quota exceeded")
	// ErrWarmQuota 预热配额不足。
	ErrWarmQuota = errors.New("edge: prewarm quota exceeded")
	// ErrQueueFull 入队后将超过 Lmax。
	ErrQueueFull = errors.New("edge: prewarm queue full")
)

// FreshStatus 区分不存在、过期与新鲜。
type FreshStatus int

const (
	StatusMissing FreshStatus = iota
	StatusStale
	StatusFresh
)

// ControlPlane 编排 quota 与 purge，持有缓存与预热队列。
type ControlPlane struct {
	registry *quota.Registry
	rules    *purge.Store
	lmax     int

	mu     sync.Mutex
	maxNow int64
	epoch  int64
	cache  map[string]map[string]int64 // tenant -> url -> filled epoch
	queue  []qitem
	queued map[string]map[string]struct{}
}

type qitem struct {
	tenant string
	url    string
}

// TickLists 为一次 Tick 的两个出队清单，均按出队次序排列。
type TickLists struct {
	Filled  []string
	Skipped []string
}

// New 创建控制面。lmax 为全局预热队列长度上限（1..1e6）。
func New(reg *quota.Registry, rules *purge.Store, lmax int) *ControlPlane {
	if lmax < 1 || lmax > 1_000_000 || reg == nil || rules == nil {
		return nil
	}
	return &ControlPlane{
		registry: reg,
		rules:    rules,
		lmax:     lmax,
		cache:    map[string]map[string]int64{},
		queued:   map[string]map[string]struct{}{},
	}
}

func validURL(url string) bool {
	p, err := purge.Parse(url)
	return err == nil && p.Kind == purge.KindURL
}

// advance 在持锁状态下推进时钟水位。
func (c *ControlPlane) advance(now int64) bool {
	if now > c.maxNow {
		c.maxNow = now
	}
	return true
}

// Fill 写入或覆盖缓存条目，filled 为当前纪元。
func (c *ControlPlane) Fill(now int64, tenant, url string) error {
	if now < 0 || now > 1_000_000_000_000 || tenant == "" || !validURL(url) {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.maxNow {
		return ErrClockBack
	}
	if !c.registry.Has(tenant) {
		return ErrUnknownTenant
	}
	c.advance(now)
	entries := c.cache[tenant]
	if entries == nil {
		entries = map[string]int64{}
		c.cache[tenant] = entries
	}
	entries[url] = c.epoch
	return nil
}

// freshLocked 判定新鲜度，调用方持锁。
func (c *ControlPlane) freshLocked(tenant, url string) FreshStatus {
	entries := c.cache[tenant]
	filled, ok := entries[url]
	if !ok {
		return StatusMissing
	}
	maxEpoch, _ := c.rules.MaxEpoch(tenant, url)
	if filled >= maxEpoch {
		return StatusFresh
	}
	return StatusStale
}

// Fresh 只读查询新鲜度。
func (c *ControlPlane) Fresh(now int64, tenant, url string) (FreshStatus, error) {
	if now < 0 || now > 1_000_000_000_000 || tenant == "" || !validURL(url) {
		return StatusMissing, ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.maxNow {
		return StatusMissing, ErrClockBack
	}
	if !c.registry.Has(tenant) {
		return StatusMissing, ErrUnknownTenant
	}
	// 只读查询不推进时钟水位。
	return c.freshLocked(tenant, url), nil
}

// Purge 规范化一批路径、检查配额并推进纪元。
func (c *ControlPlane) Purge(now int64, tenant string, items []string) (u, d int, err error) {
	kept, u, d, nerr := purge.Normalize(items)
	if nerr != nil || tenant == "" || now < 0 || now > 1_000_000_000_000 {
		return 0, 0, ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.maxNow {
		return 0, 0, ErrClockBack
	}
	limits, known := c.registry.Limits(tenant)
	if !known {
		return 0, 0, ErrUnknownTenant
	}
	usedU, usedD, _, uerr := c.registry.Used(tenant, now)
	if uerr != nil {
		return 0, 0, ErrUnknownTenant
	}
	// 先判 URL 配额，再判目录配额。
	if usedU+u > limits.Qu {
		return 0, 0, ErrURLQuota
	}
	if usedD+d > limits.Qd {
		return 0, 0, ErrDirQuota
	}
	c.advance(now)
	c.epoch++
	c.rules.Put(tenant, kept, c.epoch)
	c.registry.Apply(tenant, now, [3]int{u, d, 0})
	return u, d, nil
}

// Prewarm 将去重后的 URL 加入预热队列。
func (c *ControlPlane) Prewarm(now int64, tenant string, urls []string) (w int, err error) {
	if len(urls) < 1 || len(urls) > 100 || tenant == "" || now < 0 || now > 1_000_000_000_000 {
		return 0, ErrInvalidArg
	}
	dedup := make([]string, 0, len(urls))
	seen := map[string]struct{}{}
	for _, raw := range urls {
		if !validURL(raw) {
			return 0, ErrInvalidArg
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		dedup = append(dedup, raw)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.maxNow {
		return 0, ErrClockBack
	}
	limits, known := c.registry.Limits(tenant)
	if !known {
		return 0, ErrUnknownTenant
	}
	inQueue := c.queued[tenant]
	toEnqueue := make([]qitem, 0, len(dedup))
	for _, raw := range dedup {
		if _, ok := inQueue[raw]; ok {
			continue // 已在队列：不重复入队、不计费
		}
		toEnqueue = append(toEnqueue, qitem{tenant: tenant, url: raw})
	}
	w = len(toEnqueue)
	_, _, usedW, _ := c.registry.Used(tenant, now)
	if usedW+w > limits.Qw {
		return 0, ErrWarmQuota
	}
	if len(c.queue)+w > c.lmax {
		return 0, ErrQueueFull
	}
	c.advance(now)
	if inQueue == nil {
		inQueue = map[string]struct{}{}
		c.queued[tenant] = inQueue
	}
	for _, item := range toEnqueue {
		c.queue = append(c.queue, item)
		inQueue[item.url] = struct{}{}
	}
	c.registry.Apply(tenant, now, [3]int{0, 0, w})
	return w, nil
}

// Tick 按 FIFO 执行至多 budget 项预热。
func (c *ControlPlane) Tick(now int64, budget int) (TickLists, error) {
	if now < 0 || now > 1_000_000_000_000 || budget < 1 || budget > 10_000 {
		return TickLists{}, ErrInvalidArg
	}
	c.mu.Lock()
	if now < c.maxNow {
		c.mu.Unlock()
		return TickLists{}, ErrClockBack
	}
	c.advance(now)
	n := budget
	if n > len(c.queue) {
		n = len(c.queue)
	}
	popped := c.queue[:n]
	items := make([]qitem, n)
	copy(items, popped)
	c.queue = c.queue[n:]
	inQueues := c.queued
	caches := c.cache
	epoch := c.epoch

	result := TickLists{}
	for _, item := range items {
		delete(inQueues[item.tenant], item.url)
		status := StatusMissing
		if entries := caches[item.tenant]; entries != nil {
			if filled, ok := entries[item.url]; ok {
				maxEpoch, _ := c.rules.MaxEpoch(item.tenant, item.url)
				if filled >= maxEpoch {
					status = StatusFresh
				} else {
					status = StatusStale
				}
			}
		}
		if status == StatusFresh {
			result.Skipped = append(result.Skipped, item.url)
			continue
		}
		entries := caches[item.tenant]
		if entries == nil {
			entries = map[string]int64{}
			caches[item.tenant] = entries
		}
		entries[item.url] = epoch
		result.Filled = append(result.Filled, item.url)
	}
	c.mu.Unlock()
	return result, nil
}
