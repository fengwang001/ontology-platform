package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidConfig        = errors.New("ontology: invalid configuration")
	ErrInvalidArg           = errors.New("ontology: invalid argument")
	ErrInvalidTime          = errors.New("ontology: invalid time")
	ErrClockRollback        = errors.New("ontology: clock rollback")
	ErrInvalidState         = errors.New("ontology: invalid state")
	ErrNoHealthyHost        = errors.New("ontology: no healthy host")
	ErrInsufficientCapacity = errors.New("ontology: insufficient capacity")
)

type host struct {
	id      string
	weight  int64
	healthy bool
	join    int64
}

// Share 是单个实例的一份分配结果。
type Share struct {
	ID    string
	Count int64
}

// Calculator 按实例有效权重分配固定份数，带慢启动爬坡与单实例份额上限。
type Calculator struct {
	mu     sync.Mutex
	wslow  int64
	total  int64
	capY   int64
	maxNow int64
	hosts  map[string]*host
	order  []*host
}

// New 创建计算器。wslow 为慢启动窗口（1..1e9），total 为总份数（1..1e6），
// capY 为单实例份数上限（1..total）。任一越界返回 ErrInvalidConfig。
func New(wslow, total, capY int64) (*Calculator, error) {
	if wslow < 1 || wslow > 1_000_000_000 ||
		total < 1 || total > 1_000_000 ||
		capY < 1 || capY > total {
		return nil, ErrInvalidConfig
	}
	return &Calculator{
		wslow: wslow,
		total: total,
		capY:  capY,
		hosts: make(map[string]*host),
	}, nil
}

// AddHost 登记新实例，登记后健康，join=now。
func (c *Calculator) AddHost(id string, weight, now int64) error {
	if id == "" || weight < 1 || weight > 1_000_000 {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTime(now); err != nil {
		return err
	}
	if _, ok := c.hosts[id]; ok {
		return ErrInvalidState
	}
	h := &host{id: id, weight: weight, healthy: true, join: now}
	c.hosts[id] = h
	c.order = append(c.order, h)
	c.maxNow = now
	return nil
}

// SetWeight 修改权重，保留 join。
func (c *Calculator) SetWeight(id string, weight, now int64) error {
	if id == "" || weight < 1 || weight > 1_000_000 {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTime(now); err != nil {
		return err
	}
	h, ok := c.hosts[id]
	if !ok {
		return ErrInvalidState
	}
	h.weight = weight
	c.maxNow = now
	return nil
}

// SetHealth 设置健康位。仅在 unhealthy -> healthy 时把 join 重置为 now；
// 设成相同值不改变任何状态（仍推进 maxNow）。
func (c *Calculator) SetHealth(id string, healthy bool, now int64) error {
	if id == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTime(now); err != nil {
		return err
	}
	h, ok := c.hosts[id]
	if !ok {
		return ErrInvalidState
	}
	if h.healthy != healthy {
		h.healthy = healthy
		if healthy {
			h.join = now
		}
	}
	c.maxNow = now
	return nil
}

// RemoveHost 移除实例；同 id 再登记视为新实例。
func (c *Calculator) RemoveHost(id string, now int64) error {
	if id == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTime(now); err != nil {
		return err
	}
	h, ok := c.hosts[id]
	if !ok {
		return ErrInvalidState
	}
	delete(c.hosts, id)
	for i, v := range c.order {
		if v == h {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.maxNow = now
	return nil
}

// Shares 返回全部实例（含不健康实例，份数为 0）在 now 的份数，
// 结果按 id 字节序升序排列。无健康实例返回 ErrNoHealthyHost；
// 容量被上限锁死返回 ErrInsufficientCapacity。
func (c *Calculator) Shares(now int64) ([]Share, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTime(now); err != nil {
		return nil, err
	}
	c.maxNow = now

	active := make([]*host, 0, len(c.order))
	for _, h := range c.order {
		if h.healthy {
			active = append(active, h)
		}
	}
	sortHostsByID(active)
	if len(active) == 0 {
		return nil, ErrNoHealthyHost
	}

	counts := make(map[string]int64, len(c.order))
	trem := c.total
	for len(active) > 0 {
		eff := make([]int64, len(active))
		var effSum int64
		for i, h := range active {
			eff[i] = c.effectiveWeight(h, now)
			effSum += eff[i]
		}
		base := make([]int64, len(active))
		rem := make([]int64, len(active))
		var assigned int64
		for i := range active {
			v := eff[i] * trem
			base[i] = v / effSum
			rem[i] = v % effSum
			assigned += base[i]
		}
		order := remainderOrder(rem, active)
		for k := int64(0); k < trem-assigned; k++ {
			base[order[k]]++
		}

		var over []int
		for i, s := range base {
			if s > c.capY {
				over = append(over, i)
			}
		}
		if len(over) == 0 {
			for i, h := range active {
				counts[h.id] = base[i]
			}
			return c.orderedShares(counts), nil
		}
		overSet := make(map[int]bool, len(over))
		for _, i := range over {
			counts[active[i].id] = c.capY
			trem -= c.capY
			overSet[i] = true
		}
		next := make([]*host, 0, len(active)-len(over))
		for i, h := range active {
			if !overSet[i] {
				next = append(next, h)
			}
		}
		active = next
	}
	if trem > 0 {
		return nil, ErrInsufficientCapacity
	}
	return c.orderedShares(counts), nil
}

func (c *Calculator) orderedShares(counts map[string]int64) []Share {
	out := make([]Share, 0, len(c.order))
	for _, h := range c.order {
		out = append(out, Share{ID: h.id, Count: counts[h.id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c *Calculator) checkTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < c.maxNow {
		return ErrClockRollback
	}
	return nil
}

// effectiveWeight: 不健康为 0；e>=wslow 取 weight；
// 否则取 max(1, floor(weight*e/wslow))。
func (c *Calculator) effectiveWeight(h *host, now int64) int64 {
	if !h.healthy {
		return 0
	}
	e := now - h.join
	if e >= c.wslow {
		return h.weight
	}
	v := h.weight * e / c.wslow
	if v < 1 {
		return 1
	}
	return v
}

func sortHostsByID(hs []*host) {
	sort.Slice(hs, func(i, j int) bool { return hs[i].id < hs[j].id })
}

// remainderOrder 返回按余数降序、余数并列按 id 升序的实例下标顺序。
func remainderOrder(rem []int64, hs []*host) []int {
	idx := make([]int, len(hs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		i, j := idx[a], idx[b]
		if rem[i] != rem[j] {
			return rem[i] > rem[j]
		}
		return hs[i].id < hs[j].id
	})
	return idx
}
