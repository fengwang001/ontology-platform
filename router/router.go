// Package router 实现带会话粘性与排空期的灰度切换路由器。
//
// 路由器把带会话键的请求粘到某台主机：绑定在 last+T > now 时有效
// （恰等于 last+T 时已过期）。进入排空态的主机只服务已有有效绑定，
// 不会被选为新绑定的主机；落实（settle）时，若排空主机在 now 没有
// 任何有效绑定，或 now 不小于排空起点+D，则转为已移除并删除其全部
// 绑定。已移除是终态，其 id 不可再登记。
package router

import (
	"errors"
	"sort"
	"sync"
)

const (
	minParam = 1
	maxParam = 1_000_000_000         // T、D 的上界（含）
	maxNow   = 1_000_000_000_000_000 // now 的上界（含）
)

var (
	ErrInvalidConfig = errors.New("router: 配置非法（T、D 须在 [1, 1e9]）")

	ErrEmptyHostID = errors.New("router: 主机 id 为空")
	ErrHostExists  = errors.New("router: 主机 id 已存在（含已移除）")

	ErrEmptyKey      = errors.New("router: 会话键为空串")
	ErrHostNotFound  = errors.New("router: 主机不存在")
	ErrInvalidTime   = errors.New("router: now 非法（须在 [0, 1e15]）")
	ErrClockRollback = errors.New("router: 时钟回退")

	ErrNoActiveHost  = errors.New("router: 无可用主机")
	ErrHostNotActive = errors.New("router: 主机不是活跃态")
)

// HostStatus 是主机的生命周期状态。
type HostStatus int

const (
	Active HostStatus = iota
	Draining
	Removed
)

func (s HostStatus) String() string {
	switch s {
	case Active:
		return "Active"
	case Draining:
		return "Draining"
	case Removed:
		return "Removed"
	}
	return "Unknown"
}

type host struct {
	status     HostStatus
	drainStart int64 // 排空起点 s，仅排空态有效
}

type binding struct {
	host string
	last int64
}

// Router 是并发安全的灰度切换路由器。所有操作互斥执行，
// 结果等价于某个串行顺序。
type Router struct {
	mu       sync.Mutex
	t        int64
	d        int64
	hosts    map[string]*host
	bindings map[string]*binding // 会话键 -> 绑定
	maxNow   int64               // 已通过时钟检查的最大 now，初值 0
}

// NewRouter 以会话空闲时长 T 与排空期限 D 构造路由器。
// T 或 D 不在 [1, 1e9] 时整体拒绝并返回 ErrInvalidConfig。
func NewRouter(t, d int64) (*Router, error) {
	if t < minParam || t > maxParam || d < minParam || d > maxParam {
		return nil, ErrInvalidConfig
	}
	return &Router{
		t:        t,
		d:        d,
		hosts:    make(map[string]*host),
		bindings: make(map[string]*binding),
	}, nil
}

// AddHost 登记一台活跃态主机。id 为空返回 ErrEmptyHostID；
// id 已存在（含已移除）返回 ErrHostExists。
func (r *Router) AddHost(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" {
		return ErrEmptyHostID
	}
	if _, ok := r.hosts[id]; ok {
		return ErrHostExists
	}
	r.hosts[id] = &host{status: Active}
	return nil
}

// Route 把会话键 key 路由到某台主机。绑定有效则返回其主机并刷新
// last=now；否则丢弃该绑定，在活跃主机中选有效绑定数最少者（并列取
// id 最小）建立新绑定。没有活跃主机时报 ErrNoActiveHost，但落实与
// 最大 now 的推进保留。
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

	if b, ok := r.bindings[key]; ok && b.last+r.t > now {
		b.last = now
		return b.host, nil
	}
	delete(r.bindings, key)

	best := ""
	bestCount := 0
	found := false
	for id, h := range r.hosts {
		if h.status != Active {
			continue
		}
		c := r.liveLocked(id, now)
		if !found || c < bestCount || (c == bestCount && id < best) {
			best, bestCount, found = id, c, true
		}
	}
	if !found {
		return "", ErrNoActiveHost
	}
	r.bindings[key] = &binding{host: best, last: now}
	return best, nil
}

// Drain 把活跃主机转为排空态并记排空起点 s=now。先落实，再要求主机
// 为活跃态，转为排空态后再落实一次（排空瞬间没有有效绑定的主机立即
// 被移除）。主机不是活跃态时报 ErrHostNotActive，但落实与最大 now
// 的推进保留。
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
	if h.status != Active {
		return ErrHostNotActive
	}
	h.status = Draining
	h.drainStart = now
	r.settle(now)
	return nil
}

// Status 先落实再返回主机的状态（活跃、排空或已移除）。
func (r *Router) Status(id string, now int64) (HostStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hosts[id]
	if !ok {
		return Removed, ErrHostNotFound
	}
	if err := r.checkClock(now); err != nil {
		return Removed, err
	}
	r.maxNow = now
	r.settle(now)
	return h.status, nil
}

// Live 先落实再返回该主机在 now 的有效绑定数。
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

// checkClock 依次检查时间非法与时钟回退。
func (r *Router) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < r.maxNow {
		return ErrClockRollback
	}
	return nil
}

// settle 对每台排空态主机按 id 升序检查：若它在 now 没有任何有效
// 绑定，或 now 不小于 s+D，则转为已移除并删除其全部绑定。
func (r *Router) settle(now int64) {
	var draining []string
	for id, h := range r.hosts {
		if h.status == Draining {
			draining = append(draining, id)
		}
	}
	sort.Strings(draining)
	for _, id := range draining {
		h := r.hosts[id]
		if r.liveLocked(id, now) == 0 || now >= h.drainStart+r.d {
			h.status = Removed
			for key, b := range r.bindings {
				if b.host == id {
					delete(r.bindings, key)
				}
			}
		}
	}
}

// liveLocked 返回主机在 now 的有效绑定数。
func (r *Router) liveLocked(id string, now int64) int {
	n := 0
	for _, b := range r.bindings {
		if b.host == id && b.last+r.t > now {
			n++
		}
	}
	return n
}
