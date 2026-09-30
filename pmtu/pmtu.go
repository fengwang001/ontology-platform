// Package pmtu 实现按目的地记录的路径 MTU 发现缓存。
package pmtu

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// 构造与报告校验的拒绝原因。
var (
	ErrFloorNonPositive = errors.New("pmtu: 下限 Lo 必须为正")
	ErrMTUBelowFloor    = errors.New("pmtu: 出接口 MTU E 小于下限 Lo")
	ErrPlateausInvalid  = errors.New("pmtu: 台阶表 P 必须严格降序且各项落在 [Lo, E]")
	ErrTTLNonPositive   = errors.New("pmtu: 有效期 X 必须为正")
	ErrThresholdNonPos  = errors.New("pmtu: 黑洞阈值 K 必须为正")
	ErrClockRollback    = errors.New("pmtu: 时钟回拨")
	ErrEmptyDestination = errors.New("pmtu: 目的地为空")
	ErrSizeOutOfRange   = errors.New("pmtu: 包长 size 必须为正且不大于 E")
	ErrReportMTUInvalid = errors.New("pmtu: 报告 MTU m 不能为负或大于 E")
)

type entry struct {
	pmtu     int       // 当前路径 MTU
	setAt    time.Time // 设定时刻
	timeouts int       // 连续超时计数
}

// Cache 是并发安全的路径 MTU 发现缓存。
type Cache struct {
	mu        sync.Mutex
	mtuE      int
	floorLo   int
	plateaus  []int
	ttl       time.Duration
	threshold int
	entries   map[string]*entry
	lastTime  time.Time
	hasTime   bool
}

// NewCache 构造缓存。拒绝按此顺序只报第一个错误：
// Lo 非正、E 小于 Lo、P 不严格降序或有项越界、X 或 K 非正。
func NewCache(mtuE, floorLo int, plateaus []int, ttl time.Duration, threshold int) (*Cache, error) {
	if floorLo <= 0 {
		return nil, ErrFloorNonPositive
	}
	if mtuE < floorLo {
		return nil, ErrMTUBelowFloor
	}
	for i, p := range plateaus {
		if p < floorLo || p > mtuE {
			return nil, fmt.Errorf("%w: 第 %d 项 %d 越界", ErrPlateausInvalid, i, p)
		}
		if i > 0 && plateaus[i-1] <= p {
			return nil, fmt.Errorf("%w: 第 %d 项 %d 未严格小于前项 %d", ErrPlateausInvalid, i, p, plateaus[i-1])
		}
	}
	if ttl <= 0 {
		return nil, ErrTTLNonPositive
	}
	if threshold <= 0 {
		return nil, ErrThresholdNonPos
	}
	cp := make([]int, len(plateaus))
	copy(cp, plateaus)
	return &Cache{
		mtuE:      mtuE,
		floorLo:   floorLo,
		plateaus:  cp,
		ttl:       ttl,
		threshold: threshold,
		entries:   make(map[string]*entry),
	}, nil
}

// live 返回目的地的有效条目；条目不存在或已到期时返回 nil。
// 调用方须持有 c.mu。
func (c *Cache) live(dest string, now time.Time) *entry {
	e, ok := c.entries[dest]
	if !ok {
		return nil
	}
	if !now.Before(e.setAt.Add(c.ttl)) {
		delete(c.entries, dest)
		return nil
	}
	return e
}

// checkReport 只读校验三种报告的公共入参，不改变任何状态。
// 调用方须持有 c.mu。
func (c *Cache) checkReport(dest string, size int, now time.Time) error {
	if c.hasTime && now.Before(c.lastTime) {
		return ErrClockRollback
	}
	if dest == "" {
		return ErrEmptyDestination
	}
	if size <= 0 || size > c.mtuE {
		return ErrSizeOutOfRange
	}
	return nil
}

// commitClock 在报告通过全部校验后推进上次操作时刻。调用方须持有 c.mu。
func (c *Cache) commitClock(now time.Time) {
	c.lastTime = now
	c.hasTime = true
}

// stepDown 返回 P 中严格小于 v 的最大项，无则返回 Lo。
func (c *Cache) stepDown(v int) int {
	for _, p := range c.plateaus {
		if p < v {
			return p
		}
	}
	return c.floorLo
}

// Query 返回目的地当前的路径 MTU，无条目或条目已到期时返回 E。
func (c *Cache) Query(dest string, now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.live(dest, now); e != nil {
		return e.pmtu
	}
	return c.mtuE
}

// ReportFragmentation 处理「需分片」报告：m 为 0 或不小于 size 视为未报告，
// 未报告时取 P 中严格小于 size 的最大项（无则取 Lo）作为 m；
// 新值为 max(m, Lo)，不小于当前路径 MTU 则忽略且不刷新设定时刻。
func (c *Cache) ReportFragmentation(dest string, size, m int, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkReport(dest, size, now); err != nil {
		return err
	}
	if m < 0 || m > c.mtuE {
		return ErrReportMTUInvalid
	}
	c.commitClock(now)
	e := c.live(dest, now)
	if e == nil {
		e = &entry{pmtu: c.mtuE, setAt: now}
		c.entries[dest] = e
	}
	if m == 0 || m >= size {
		m = c.stepDown(size)
	}
	if m < c.floorLo {
		m = c.floorLo
	}
	if m >= e.pmtu {
		return nil
	}
	e.pmtu = m
	e.setAt = now
	return nil
}

// ReportTimeout 处理超时报告：仅当 size 等于当前路径 MTU 时计数加一；
// 计数达 K 时沿台阶表降级、计数清零并把设定时刻置为现在。
func (c *Cache) ReportTimeout(dest string, size int, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkReport(dest, size, now); err != nil {
		return err
	}
	c.commitClock(now)
	e := c.live(dest, now)
	cur := c.mtuE
	if e != nil {
		cur = e.pmtu
	}
	if size != cur {
		return nil
	}
	if e == nil {
		e = &entry{pmtu: c.mtuE, setAt: now}
		c.entries[dest] = e
	}
	e.timeouts++
	if e.timeouts >= c.threshold {
		e.pmtu = c.stepDown(e.pmtu)
		e.timeouts = 0
		e.setAt = now
	}
	return nil
}

// ReportSuccess 处理成功报告：仅当 size 等于当前路径 MTU 时计数清零。
func (c *Cache) ReportSuccess(dest string, size int, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkReport(dest, size, now); err != nil {
		return err
	}
	c.commitClock(now)
	if e := c.live(dest, now); e != nil && size == e.pmtu {
		e.timeouts = 0
	}
	return nil
}
