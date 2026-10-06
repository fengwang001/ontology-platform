// Package member 维护按端口的组成员关系与离组末成员查询计划。
//
// 成员关系只存到期时刻 exp（GMI），到期用全局最小堆惰性弹出，不做全表
// 扫描；离组（非 fastLeave）只降低 exp 并安排 Rb 条特定组查询，等末成员
// 查询窗口自然到期。
package member

import (
	"container/heap"
	"errors"
	"sort"
)

var (
	// ErrNotMember 表示对非成员（组，端口）执行了 Leave。
	ErrNotMember = errors.New("member: not a member")
	// ErrPortLimit 表示该端口未到期的成员关系数已达上限。
	ErrPortLimit = errors.New("member: port membership limit reached")
	// ErrGroupLimit 表示有成员的组数已达上限。
	ErrGroupLimit = errors.New("member: group limit reached")
)

// SpecQuery 是一条计划中的特定组查询。
type SpecQuery struct {
	Time  int64
	Group uint32
	Port  int
}

type specEntry struct {
	SpecQuery
	cancelled bool
}

type key struct {
	group uint32
	port  int
}

type expiryItem struct {
	exp   int64
	group uint32
	port  int
}

// expiryHeap 是按 exp 排序的最小堆；刷新或降低 exp 时直接压入新项，
// 旧项弹出时因与当前 exp 不符而被丢弃（惰性删除）。
type expiryHeap []expiryItem

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].exp < h[j].exp }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)        { *h = append(*h, x.(expiryItem)) }
func (h *expiryHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

type groupState struct {
	members map[int]int64 // 端口 -> 到期时刻
}

func (g *groupState) liveCount(now int64) int {
	n := 0
	for _, exp := range g.members {
		if exp > now {
			n++
		}
	}
	return n
}

// Members 是组成员关系表。它不是并发安全的，调用方必须串行化访问。
type Members struct {
	gmi  int64
	rb   int64
	lmqi int64
	lp   int
	gmax int

	groups   map[uint32]*groupState
	expiring expiryHeap
	pending  map[key][]*specEntry // 每(组,端口)尚未到时刻的特定组查询
	queue    []*specEntry         // 全局待发特定组查询
}

// New 构造成员关系表，gmi 为成员有效期，rb 为健壮系数，lmqi 为末成员
// 查询间隔，lp 为每端口成员关系上限，gmax 为全机组数上限。
func New(gmi, rb, lmqi int64, lp, gmax int) *Members {
	return &Members{
		gmi:     gmi,
		rb:      rb,
		lmqi:    lmqi,
		lp:      lp,
		gmax:    gmax,
		groups:  make(map[uint32]*groupState),
		pending: make(map[key][]*specEntry),
	}
}

// Expire 落地所有 exp <= now 的成员关系（恰等即到期）。仅在操作被接受
// 后调用；开销与到期条目数成正比，与组总数无关。
func (m *Members) Expire(now int64) {
	for len(m.expiring) > 0 && m.expiring[0].exp <= now {
		it := heap.Pop(&m.expiring).(expiryItem)
		g, ok := m.groups[it.group]
		if !ok {
			continue
		}
		exp, ok := g.members[it.port]
		if !ok || exp != it.exp {
			continue // 陈旧堆项：已刷新、已降低或已移除
		}
		delete(g.members, it.port)
		if len(g.members) == 0 {
			delete(m.groups, it.group)
		}
	}
}

func (m *Members) portLiveCount(port int, now int64) int {
	n := 0
	for _, g := range m.groups {
		if exp, ok := g.members[port]; ok && exp > now {
			n++
		}
	}
	return n
}

func (m *Members) liveGroupCount(now int64) int {
	n := 0
	for _, g := range m.groups {
		if g.liveCount(now) > 0 {
			n++
		}
	}
	return n
}

// cancelAfter 取消 (组,端口) 时刻严格大于 now 的特定组查询：
// 同刻及时刻已过的查询照发（先发后取消）。
func (m *Members) cancelAfter(k key, now int64) {
	for _, e := range m.pending[k] {
		if e.Time > now {
			e.cancelled = true
		}
	}
	delete(m.pending, k)
}

// Report 使（组，端口）成为成员或刷新既有成员关系。
// 端口超限先于组超限；已到期（exp <= now）的成员关系不计入。
func (m *Members) Report(group uint32, port int, now int64) error {
	k := key{group, port}
	g := m.groups[group]
	if g != nil {
		if exp, ok := g.members[port]; ok && exp > now {
			g.members[port] = now + m.gmi
			heap.Push(&m.expiring, expiryItem{exp: now + m.gmi, group: group, port: port})
			m.cancelAfter(k, now)
			return nil
		}
	}
	if m.portLiveCount(port, now) >= m.lp {
		return ErrPortLimit
	}
	if (g == nil || g.liveCount(now) == 0) && m.liveGroupCount(now) >= m.gmax {
		return ErrGroupLimit
	}
	if g == nil {
		g = &groupState{members: make(map[int]int64)}
		m.groups[group] = g
	}
	g.members[port] = now + m.gmi
	heap.Push(&m.expiring, expiryItem{exp: now + m.gmi, group: group, port: port})
	m.cancelAfter(k, now)
	return nil
}

// Leave 处理离组。fastLeave 端口立即移除；本机为查询器时降低 exp 并
// 安排 Rb 条特定组查询；已有未发完的特定组查询时不重排、exp 不变；
// 本机不是查询器时接受但无任何效果。
func (m *Members) Leave(group uint32, port int, now int64, fastLeave, querier bool) error {
	g := m.groups[group]
	if g == nil {
		return ErrNotMember
	}
	exp, ok := g.members[port]
	if !ok || exp <= now {
		return ErrNotMember
	}
	k := key{group, port}
	if fastLeave {
		delete(g.members, port)
		if len(g.members) == 0 {
			delete(m.groups, group)
		}
		m.cancelAfter(k, now)
		return nil
	}
	if !querier {
		return nil
	}
	for _, e := range m.pending[k] {
		if e.Time >= now {
			return nil
		}
	}
	newExp := now + m.rb*m.lmqi
	if exp < newExp {
		newExp = exp // 只降不升
	}
	g.members[port] = newExp
	heap.Push(&m.expiring, expiryItem{exp: newExp, group: group, port: port})
	var entries []*specEntry
	for i := int64(0); i < m.rb; i++ {
		t := now + i*m.lmqi
		if t >= newExp {
			break // 时刻不小于 exp 的特定组查询随成员到期而取消
		}
		e := &specEntry{SpecQuery: SpecQuery{Time: t, Group: group, Port: port}}
		entries = append(entries, e)
		m.queue = append(m.queue, e)
	}
	m.pending[k] = entries
	return nil
}

// CancelAllAfter 让位时取消全部时刻严格大于 now 的特定组查询。
func (m *Members) CancelAllAfter(now int64) {
	for k := range m.pending {
		m.cancelAfter(k, now)
	}
}

// Ports 返回 group 的成员端口（升序）与触达的成员记录数。
// 调用前必须先 Expire(now)，保证表中只剩未到期记录。
func (m *Members) Ports(group uint32, now int64) ([]int, int) {
	g := m.groups[group]
	if g == nil {
		return nil, 0
	}
	ports := make([]int, 0, len(g.members))
	for p := range g.members {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports, len(g.members)
}

// DrainSpecific 取出时刻不大于 now 且未取消的特定组查询并清理队列。
// 队列不变式：queue 中的条目都是尚未发射的（每次调用后只剩时刻大于
// now 的条目），因此晚于上次 Drain 才调度、时刻不超过 now 的条目也会
// 被正确发射。
func (m *Members) DrainSpecific(now int64) []SpecQuery {
	keep := m.queue[:0]
	var due []*specEntry
	for _, e := range m.queue {
		switch {
		case e.cancelled:
			// 已取消的条目直接丢弃
		case e.Time <= now:
			due = append(due, e)
		default:
			keep = append(keep, e)
		}
	}
	m.queue = keep
	sort.Slice(due, func(i, j int) bool {
		a, b := due[i].SpecQuery, due[j].SpecQuery
		if a.Time != b.Time {
			return a.Time < b.Time
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Port < b.Port
	})
	out := make([]SpecQuery, 0, len(due))
	for _, e := range due {
		out = append(out, e.SpecQuery)
	}
	return out
}
