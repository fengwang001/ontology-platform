// Package keytable 按租户维护带容量上限的指纹键表并组合采样与摘要交付。
package keytable

import (
	"sync"

	"ontology/report"
	"ontology/window"
)

// Sampler 是线程安全的日志限频采样器。
// 零值不可用，须经 New 构造。sink 在持锁状态下被同步调用，
// 因此 sink 内不得再调用本对象的任何方法（不可重入）。
type Sampler struct {
	mu       sync.Mutex
	params   window.Params
	w        int64
	kt       int
	tmax     int
	maxNow   int64
	tenants  map[string]*tenant
	examined int64
}

// tenant 是单个租户的键表：map 加访问序双向链表（oldest→newest）。
type tenant struct {
	nodes          map[string]*node
	oldest, newest *node
	count          int
}

// node 是链表节点，携带该键的窗口采样状态。
type node struct {
	key        string
	e          window.Entry
	prev, next *node
}

// New 构造采样器；参数越界返回 report.ErrInvalidArgument。
func New(n, m, windowMS, perTenant, maxTenants int64) (*Sampler, error) {
	if n < 0 || n > 1_000_000 || m < 1 || m > 1_000_000 ||
		windowMS < 1 || windowMS > 1_000_000_000 ||
		perTenant < 1 || perTenant > 100_000 ||
		maxTenants < 1 || maxTenants > 10_000 {
		return nil, report.ErrInvalidArgument
	}
	return &Sampler{
		params:  window.Params{N: n, M: m},
		w:       windowMS,
		kt:      int(perTenant),
		tmax:    int(maxTenants),
		tenants: make(map[string]*tenant),
	}, nil
}

// Decision 是一次 Record 的结果。
type Decision struct {
	Kept      bool // 被拒绝时 Err 非空，其余字段为零值。
	Summaries []report.Summary
	Err       error
}

// unlink 从访问序链表摘除节点。
func (t *tenant) unlink(n *node) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		t.oldest = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		t.newest = n.prev
	}
	n.prev, n.next = nil, nil
	t.count--
}

// pushBack 把节点放到链表尾部，使其成为最新访问。
func (t *tenant) pushBack(n *node) {
	n.prev = t.newest
	if t.newest != nil {
		t.newest.next = n
	} else {
		t.oldest = n
	}
	t.newest = n
	t.count++
}

// Record 处理一条日志；拒绝顺序：参数非法 → 时钟回退 → 租户上限。
func (s *Sampler) Record(now int64, tenantName, key string, sev int) Decision {
	if tenantName == "" || len(tenantName) > 64 || key == "" || len(key) > 64 ||
		sev < 0 || sev > 5 || now < 0 || now > 1_000_000_000_000 {
		return Decision{Err: report.ErrInvalidArgument}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.maxNow {
		return Decision{Err: report.ErrClockSkew}
	}
	t, ok := s.tenants[tenantName]
	if !ok {
		if len(s.tenants) >= s.tmax {
			return Decision{Err: report.ErrTenantLimit}
		}
		t = &tenant{nodes: make(map[string]*node)}
		s.tenants[tenantName] = t
	}

	s.maxNow = now
	cur := now / s.w
	d := Decision{}

	n, hit := t.nodes[key]
	if hit {
		s.examined++
		t.unlink(n)
		t.pushBack(n)
		if oldWin, dropped, had := n.e.RollOver(cur); had {
			d.Summaries = append(d.Summaries, report.Summary{
				Tenant: tenantName, Key: key, Window: oldWin,
				Dropped: dropped, Reason: report.Rolled,
			})
		}
	} else {
		if t.count >= s.kt {
			victim := t.oldest
			s.examined++
			t.unlink(victim)
			delete(t.nodes, victim.key)
			if victim.e.Dropped > 0 {
				d.Summaries = append(d.Summaries, report.Summary{
					Tenant: tenantName, Key: victim.key, Window: victim.e.Win,
					Dropped: victim.e.Dropped, Reason: report.Evicted,
				})
			}
		}
		n = &node{key: key, e: window.Entry{Win: cur}}
		t.nodes[key] = n
		t.pushBack(n)
	}

	if sev >= 4 {
		d.Kept = true
	} else {
		d.Kept = n.e.Observe(s.params)
	}
	return d
}
