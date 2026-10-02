// Package router 实现带会话粘性与排空期的灰度切换路由器。
package router

import (
	"errors"
	"sort"
	"sync"
)

const (
	minTD  = int64(1)
	maxTD  = int64(1_000_000_000)
	maxNow = int64(1_000_000_000_000_000)
)

// HostStatus 表示主机生命周期状态。
type HostStatus int

const (
	StatusActive HostStatus = iota
	StatusDraining
	StatusRemoved
)

func (s HostStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusDraining:
		return "draining"
	case StatusRemoved:
		return "removed"
	}
	return "unknown"
}

var (
	ErrInvalidConfig   = errors.New("router: T 与 D 必须在 [1, 1e9]")
	ErrEmptyHostID     = errors.New("router: 主机 id 为空")
	ErrHostExists      = errors.New("router: 主机 id 已存在（含已移除）")
	ErrEmptyKey        = errors.New("router: 会话键为空")
	ErrHostNotFound    = errors.New("router: 主机不存在")
	ErrInvalidTime     = errors.New("router: now 必须在 [0, 1e15]")
	ErrClockRegression = errors.New("router: 时钟回退")
	ErrNoActiveHost    = errors.New("router: 无可用主机")
	ErrHostNotActive   = errors.New("router: 主机不是活跃态")
)

type host struct {
	status     HostStatus
	drainStart int64
}

type binding struct {
	host string
	last int64
}

// Router 是并发安全的灰度切换路由器。
type Router struct {
	mu       sync.Mutex
	t        int64
	d        int64
	hosts    map[string]*host
	bindings map[string]*binding
	maxNow   int64
}

// NewRouter 构造路由器，T 为会话空闲时长，D 为排空期限，均须在 [1, 1e9]。
func NewRouter(t, d int64) (*Router, error) {
	if t < minTD || t > maxTD || d < minTD || d > maxTD {
		return nil, ErrInvalidConfig
	}
	return &Router{
		t:        t,
		d:        d,
		hosts:    make(map[string]*host),
		bindings: make(map[string]*binding),
	}, nil
}

// AddHost 登记一台活跃主机。
func (r *Router) AddHost(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" {
		return ErrEmptyHostID
	}
	if _, ok := r.hosts[id]; ok {
		return ErrHostExists
	}
	r.hosts[id] = &host{status: StatusActive}
	return nil
}

// Drain 把活跃主机转为排空态。
func (r *Router) Drain(id string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	r.maxNow = now
	r.settle(now)
	if h.status != StatusActive {
		return ErrHostNotActive
	}
	h.status = StatusDraining
	h.drainStart = now
	r.settle(now)
	return nil
}

// Route 为会话键选择主机。
func (r *Router) Route(key string, now int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == "" {
		return "", ErrEmptyKey
	}
	if err := r.checkClock(now); err != nil {
		return "", err
	}
	r.maxNow = now
	r.settle(now)
	if b, ok := r.bindings[key]; ok {
		if r.valid(b, now) {
			b.last = now
			return b.host, nil
		}
		delete(r.bindings, key)
	}
	id, ok := r.pickActive(now)
	if !ok {
		return "", ErrNoActiveHost
	}
	r.bindings[key] = &binding{host: id, last: now}
	return id, nil
}

// Status 返回主机状态。
func (r *Router) Status(id string, now int64) (HostStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hosts[id]
	if !ok {
		return StatusActive, ErrHostNotFound
	}
	if err := r.checkClock(now); err != nil {
		return StatusActive, err
	}
	r.maxNow = now
	r.settle(now)
	return h.status, nil
}

// Live 返回主机的有效绑定数。
func (r *Router) Live(id string, now int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.hosts[id]; !ok {
		return 0, ErrHostNotFound
	}
	if err := r.checkClock(now); err != nil {
		return 0, err
	}
	r.maxNow = now
	r.settle(now)
	return r.liveLocked(id, now), nil
}

// checkClock 依次校验时间合法性与时钟回退，不加锁，调用方须持锁。
func (r *Router) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < r.maxNow {
		return ErrClockRegression
	}
	return nil
}

// valid 判断绑定在 now 是否有效：last+T 严格大于 now。
func (r *Router) valid(b *binding, now int64) bool {
	return b.last+r.t > now
}

// settle 按 id 升序落实排空态主机：无有效绑定或到达排空期限则移除。
func (r *Router) settle(now int64) {
	var draining []string
	for id, h := range r.hosts {
		if h.status == StatusDraining {
			draining = append(draining, id)
		}
	}
	sort.Strings(draining)
	for _, id := range draining {
		h := r.hosts[id]
		if r.liveLocked(id, now) == 0 || now >= h.drainStart+r.d {
			h.status = StatusRemoved
			for key, b := range r.bindings {
				if b.host == id {
					delete(r.bindings, key)
				}
			}
		}
	}
}

// liveLocked 统计主机在 now 的有效绑定数。
func (r *Router) liveLocked(id string, now int64) int {
	n := 0
	for _, b := range r.bindings {
		if b.host == id && r.valid(b, now) {
			n++
		}
	}
	return n
}

// pickActive 在活跃主机中选有效绑定数最少者，并列取 id 最小。
func (r *Router) pickActive(now int64) (string, bool) {
	var active []string
	for id, h := range r.hosts {
		if h.status == StatusActive {
			active = append(active, id)
		}
	}
	if len(active) == 0 {
		return "", false
	}
	sort.Strings(active)
	best := active[0]
	bestLive := r.liveLocked(best, now)
	for _, id := range active[1:] {
		if n := r.liveLocked(id, now); n < bestLive {
			best, bestLive = id, n
		}
	}
	return best, true
}
