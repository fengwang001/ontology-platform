// Package shares 实现带慢启动爬坡与单实例份额上限的实例流量份额计算器。
package shares

import (
	"errors"
	"sort"
	"sync"
)

// 计算器与操作可能返回的错误。
var (
	ErrInvalidConfig        = errors.New("shares: invalid config")
	ErrInvalidParam         = errors.New("shares: invalid parameter")
	ErrInvalidTime          = errors.New("shares: invalid time")
	ErrClockRollback        = errors.New("shares: clock rollback")
	ErrHostExists           = errors.New("shares: host already exists")
	ErrHostNotFound         = errors.New("shares: host not found")
	ErrNoHealthyHosts       = errors.New("shares: no healthy hosts")
	ErrInsufficientCapacity = errors.New("shares: insufficient capacity")
)

const (
	minWSlow  = 1
	maxWSlow  = 1_000_000_000
	minTotal  = 1
	maxTotal  = 1_000_000
	minWeight = 1
	maxWeight = 1_000_000
	maxTime   = 1_000_000_000_000_000
)

// Share 是单个实例的份数结果，Shares 按 id 字节序升序返回。
type Share struct {
	ID    string
	Value int64
}

type host struct {
	weight  int64
	join    int64
	healthy bool
}

// Calculator 是实例流量份额计算器，所有方法可并发调用，
// 效果等价于某个串行顺序。
type Calculator struct {
	mu     sync.Mutex
	wSlow  int64
	total  int64
	capPer int64
	maxNow int64
	hosts  map[string]*host
}

// NewCalculator 构造计算器。Wslow 取值 1 到 1e9，T 取值 1 到 1e6，
// Y 取值 1 到 T，任一越界则整体拒绝并返回 ErrInvalidConfig。
func NewCalculator(wSlow, total, capPer int64) (*Calculator, error) {
	if wSlow < minWSlow || wSlow > maxWSlow ||
		total < minTotal || total > maxTotal ||
		capPer < 1 || capPer > total {
		return nil, ErrInvalidConfig
	}
	return &Calculator{
		wSlow:  wSlow,
		total:  total,
		capPer: capPer,
		hosts:  make(map[string]*host),
	}, nil
}

// AddHost 登记实例，登记后为健康且上线时刻 join=now。
func (c *Calculator) AddHost(id string, weight int64, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := checkParam(id, weight); err != nil {
		return err
	}
	if err := c.checkTime(now); err != nil {
		return err
	}
	if _, ok := c.hosts[id]; ok {
		return ErrHostExists
	}
	c.accept(now)
	c.hosts[id] = &host{weight: weight, join: now, healthy: true}
	return nil
}

// SetWeight 只改权重，保留 join。
func (c *Calculator) SetWeight(id string, weight int64, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := checkParam(id, weight); err != nil {
		return err
	}
	if err := c.checkTime(now); err != nil {
		return err
	}
	h, ok := c.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	c.accept(now)
	h.weight = weight
	return nil
}

// SetHealth 设置健康位，仅当从不健康变为健康时把 join 重置为 now。
func (c *Calculator) SetHealth(id string, healthy bool, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := checkID(id); err != nil {
		return err
	}
	if err := c.checkTime(now); err != nil {
		return err
	}
	h, ok := c.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	c.accept(now)
	if healthy && !h.healthy {
		h.join = now
	}
	h.healthy = healthy
	return nil
}

// RemoveHost 移除实例，同一 id 再登记视为新实例。
func (c *Calculator) RemoveHost(id string, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := checkID(id); err != nil {
		return err
	}
	if err := c.checkTime(now); err != nil {
		return err
	}
	if _, ok := c.hosts[id]; !ok {
		return ErrHostNotFound
	}
	c.accept(now)
	delete(c.hosts, id)
	return nil
}

// Shares 返回按 id 字节序升序的全部实例份数。
func (c *Calculator) Shares(now int64) ([]Share, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTime(now); err != nil {
		return nil, err
	}
	c.accept(now)

	ids := make([]string, 0, len(c.hosts))
	for id := range c.hosts {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]Share, 0, len(ids))
	type active struct {
		id  string
		eff int64
	}
	act := make([]active, 0, len(ids))
	for _, id := range ids {
		h := c.hosts[id]
		eff := c.effective(h, now)
		result = append(result, Share{ID: id})
		if h.healthy {
			act = append(act, active{id: id, eff: eff})
		}
	}
	if len(act) == 0 {
		return nil, ErrNoHealthyHosts
	}

	final := make(map[string]int64, len(act))
	tRem := c.total
	// 每轮至少固定一个实例，故轮数不超过健康实例数。
	for len(act) > 0 {
		var sumEff int64
		for _, a := range act {
			sumEff += a.eff
		}
		// 最大余数法：底数 eff*tRem/sumEff，余数 (eff*tRem)%sumEff
		// 从大到小、并列取 id 小者，每个实例至多加一份。
		type rem struct {
			id   string
			base int64
			r    int64
		}
		rs := make([]rem, len(act))
		var baseSum int64
		for i, a := range act {
			p := a.eff * tRem
			rs[i] = rem{id: a.id, base: p / sumEff, r: p % sumEff}
			baseSum += rs[i].base
		}
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].r != rs[j].r {
				return rs[i].r > rs[j].r
			}
			return rs[i].id < rs[j].id
		})
		round := make(map[string]int64, len(act))
		for _, r := range rs {
			round[r.id] = r.base
		}
		for i := int64(0); i < tRem-baseSum; i++ {
			round[rs[i].id]++
		}

		var capped []string
		for _, a := range act {
			if round[a.id] > c.capPer {
				capped = append(capped, a.id)
			}
		}
		if len(capped) == 0 {
			for id, v := range round {
				final[id] = v
			}
			tRem = 0
			break
		}
		// 本轮所有超限实例一次性固定为 Y 并从活动集合移除。
		cappedSet := make(map[string]bool, len(capped))
		for _, id := range capped {
			cappedSet[id] = true
			final[id] = c.capPer
			tRem -= c.capPer
		}
		next := act[:0]
		for _, a := range act {
			if !cappedSet[a.id] {
				next = append(next, a)
			}
		}
		act = next
	}
	if tRem > 0 {
		return nil, ErrInsufficientCapacity
	}

	for i := range result {
		result[i].Value = final[result[i].ID]
	}
	return result, nil
}

// effective 计算实例在 now 的有效权重：不健康为 0；健康时令
// e=now-join，e>=Wslow 取 weight，否则取 max(1, weight*e/Wslow)。
func (c *Calculator) effective(h *host, now int64) int64 {
	if !h.healthy {
		return 0
	}
	e := now - h.join
	if e >= c.wSlow {
		return h.weight
	}
	// 此分支 e < Wslow <= 1e9，weight <= 1e6，乘积不溢出。
	v := h.weight * e / c.wSlow
	if v < 1 {
		v = 1
	}
	return v
}

func checkID(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	return nil
}

func checkParam(id string, weight int64) error {
	if id == "" || weight < minWeight || weight > maxWeight {
		return ErrInvalidParam
	}
	return nil
}

func (c *Calculator) checkTime(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < c.maxNow {
		return ErrClockRollback
	}
	return nil
}

// accept 在接受操作后推进最大 now。
func (c *Calculator) accept(now int64) {
	if now > c.maxNow {
		c.maxNow = now
	}
}
