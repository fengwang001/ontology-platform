// Package querier 实现 IGMP 风格的查询器选举、本机查询发送计划与交换机组播控制编排。
//
// 设计要点见仓库根目录 DESIGN.md：无后台 goroutine，全部状态按 now 在操作入口惰性推进；
// 查询器连续任职时段用 epoch（半开区间 [start,end)）描述，通用查询时刻由 epoch 起点
// 按 QI 惰性推算；特定组查询与路由器端口各自维护最小堆；单一互斥锁保证并发等价串行。
package querier

import (
	"container/heap"
	"math"
	"sort"
	"sync"

	"ontology/forward"
	"ontology/member"
)

// QueryKind 标识一条本机查询的种类。
type QueryKind int

const (
	// General 通用查询，输出中的组与端口记 0。
	General QueryKind = iota
	// GroupSpecific 面向单端口的特定组查询。
	GroupSpecific
)

// Query 是 Drain 输出的一条本机查询。
type Query struct {
	Time  int64
	Kind  QueryKind
	Group uint32
	Port  int
}

type routerPort struct {
	port int
	exp  int64
	ver  int64
}

type routerHeapItem struct {
	port int
	exp  int64
	ver  int64
}

type routerHeap []routerHeapItem

func (h routerHeap) Len() int           { return len(h) }
func (h routerHeap) Less(i, j int) bool { return h[i].exp < h[j].exp }
func (h routerHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *routerHeap) Push(x any)        { *h = append(*h, x.(routerHeapItem)) }
func (h *routerHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

type sgQuery struct {
	time  int64
	group uint32
	port  int
}

type sgHeap []sgQuery

func (h sgHeap) Len() int { return len(h) }
func (h sgHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	if h[i].group != h[j].group {
		return h[i].group < h[j].group
	}
	return h[i].port < h[j].port
}
func (h sgHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *sgHeap) Push(x any)   { *h = append(*h, x.(sgQuery)) }
func (h *sgHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

type epoch struct {
	start   int64
	end     int64 // math.MaxInt64 表示仍开放。
	emitted int64 // 已进入输出缓冲的通用查询个数（k=0..emitted-1）。
}

// Controller 是整个控制器。
type Controller struct {
	mu        sync.Mutex
	p         int
	ownIP     uint32
	qi        int64
	qri       int64
	rb        int
	lmqi      int64
	gmi       int64
	oqpi      int64
	fastLeave []bool
	flood     bool
	gmax      int
	lp        int
	clock     int64

	isQuerier bool
	oq        int64 // 非查询器时他机查询器存续时刻；查询器时为 0。
	epochs    []epoch

	routers map[int]*routerPort
	routerQ routerHeap
	sgq     sgHeap // 全部待发的特定组查询（含已到点尚未 Drain 者）。
	out     []Query
	members *member.Table
}

// New 构造并校验控制器。
func New(P int, ownIP uint32, QI, QRI, Rb, LMQI int64, fastLeave []bool, floodUnknown bool, Gmax, Lp int) (*Controller, error) {
	if P < 1 || P > 256 ||
		ownIP == 0 ||
		QI < 1 || QI > 1_000_000 ||
		QRI < 1 || QRI > 1_000_000 || QRI >= QI ||
		Rb < 1 || Rb > 7 ||
		LMQI < 1 || LMQI > 1_000_000 ||
		Gmax < 0 || Lp < 0 ||
		len(fastLeave) != P {
		return nil, member.ErrInvalidParam
	}
	gmi := Rb*QI + QRI
	oqpi := Rb*QI + QRI/2
	fl := make([]bool, P+1)
	copy(fl[1:], fastLeave)
	return &Controller{
		p:         P,
		ownIP:     ownIP,
		qi:        QI,
		qri:       QRI,
		rb:        int(Rb),
		lmqi:      LMQI,
		gmi:       gmi,
		oqpi:      oqpi,
		fastLeave: fl,
		flood:     floodUnknown,
		gmax:      Gmax,
		lp:        Lp,
		isQuerier: true,
		epochs:    []epoch{{start: 0, end: math.MaxInt64}},
		routers:   make(map[int]*routerPort),
		routerQ:   make(routerHeap, 0),
		sgq:       make(sgHeap, 0),
		members:   member.New(P, gmi, Gmax, Lp),
	}, nil
}

func (c *Controller) checkClock(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return member.ErrInvalidParam
	}
	if now < c.clock {
		return member.ErrClockRollback
	}
	return nil
}

// pump 把推进到 now 的全部后果惰性落地：查询器恢复、成员与路由器到期、
// 各 epoch 的通用查询、到点特定组查询，统一进入 out（由 Drain 排序输出）。
func (c *Controller) pump(now int64) {
	// 查询器恢复先于本时刻一切输入事件：恰在 oq 先恢复并发出该时刻通用查询。
	if !c.isQuerier && now >= c.oq {
		c.isQuerier = true
		// 上一个 epoch 在让位时已关闭；这里只追加新的开放 epoch，不延长旧时段。
		c.epochs = append(c.epochs, epoch{start: c.oq, end: math.MaxInt64})
		c.oq = 0
	}

	for len(c.routerQ) > 0 && c.routerQ[0].exp <= now {
		it := heap.Pop(&c.routerQ).(routerHeapItem)
		r := c.routers[it.port]
		if r == nil || r.exp != it.exp || r.ver != it.ver {
			continue
		}
		delete(c.routers, it.port)
	}

	// 成员到期与特定组查询按时刻交错：SGQ 时刻不晚于成员到期时刻时先发查询，
	// 从而“同刻查询照发”，而恰在 exp 时刻的查询因成员先到期而被取消。
loop:
	for {
		nextMember, haveMember := c.members.NextExpiry()
		haveSG := len(c.sgq) > 0
		switch {
		case !haveMember && !haveSG:
			break loop
		case haveMember && (!haveSG || nextMember < c.sgq[0].time):
			if nextMember > now {
				break loop
			}
			c.members.PopExpiry()
		default:
			q := c.sgq[0]
			if q.time > now {
				break loop
			}
			heap.Pop(&c.sgq)
			if r, ok := c.members.Lookup(q.port, q.group); ok && r.Pending {
				c.out = append(c.out, Query{Time: q.time, Kind: GroupSpecific, Group: q.group, Port: q.port})
			}
		}
	}
	c.members.SetClock(now)

	for i := range c.epochs {
		e := &c.epochs[i]
		var lastK int64
		if e.end == math.MaxInt64 {
			if now >= e.start {
				lastK = (now-e.start)/c.qi + 1
			}
		} else {
			lastK = (e.end-1-e.start)/c.qi + 1
		}
		for e.emitted < lastK {
			c.out = append(c.out, Query{Time: e.start + e.emitted*c.qi, Kind: General})
			e.emitted++
		}
	}

	c.clock = now
}

// Report 处理从 port 收到的组成员报告，返回应转发到的端口（路由器端口去掉 port，升序）。
func (c *Controller) Report(port int, group uint32, now int64) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.members.ValidPort(port) || !member.IsMulticastGroup(group) {
		return nil, member.ErrInvalidParam
	}
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	c.pump(now)
	if member.IsLocalGroup(group) {
		return nil, member.ErrLocalGroup
	}

	wasPending, err := c.members.Report(port, group, now)
	if err != nil {
		return nil, err
	}
	if wasPending {
		c.cancelSGQ(group, port)
	}
	return c.routerPortsExcept(port), nil
}

// cancelSGQ 取消某 (组,端口) 尚未进入输出缓冲的全部特定组查询。
func (c *Controller) cancelSGQ(group uint32, port int) {
	kept := c.sgq[:0]
	for _, q := range c.sgq {
		if q.group == group && q.port == port {
			continue
		}
		kept = append(kept, q)
	}
	c.sgq = kept
	heap.Init(&c.sgq)
}

func (c *Controller) routerPortsExcept(skip int) []int {
	out := make([]int, 0, len(c.routers))
	for p := range c.routers {
		if p != skip {
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// Leave 处理从 port 收到的离组报文。
func (c *Controller) Leave(port int, group uint32, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.members.ValidPort(port) || !member.IsMulticastGroup(group) {
		return member.ErrInvalidParam
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	c.pump(now)
	if member.IsLocalGroup(group) {
		return member.ErrLocalGroup
	}
	if !c.isQuerier {
		return nil // 非查询器：接受但无效果。
	}
	if c.fastLeave[port] {
		if err := c.members.FastLeave(port, group); err != nil {
			return err
		}
		c.cancelSGQ(group, port)
		return nil
	}
	exp := now + int64(c.rb)*c.lmqi
	r, alreadyPending, err := c.members.PrepareLeave(port, group, exp)
	if err != nil {
		return err
	}
	if alreadyPending {
		return nil
	}
	for k := 0; k < c.rb; k++ {
		t := now + int64(k)*c.lmqi
		if t >= r.Exp {
			break
		}
		heap.Push(&c.sgq, sgQuery{time: t, group: group, port: port})
	}
	return nil
}

// Query 处理从 port 收到的他机通用查询。
func (c *Controller) Query(port int, srcIP uint32, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.members.ValidPort(port) || srcIP == 0 || srcIP == c.ownIP {
		return member.ErrInvalidParam
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	c.pump(now)

	r := c.routers[port]
	if r == nil {
		r = &routerPort{port: port}
		c.routers[port] = r
	}
	r.ver++
	r.exp = now + c.oqpi
	heap.Push(&c.routerQ, routerHeapItem{port: port, exp: r.exp, ver: r.ver})

	if srcIP < c.ownIP {
		if c.isQuerier {
			c.isQuerier = false
			c.epochs[len(c.epochs)-1].end = now
			c.sgq = c.sgq[:0]
			c.members.ClearPending()
		}
		c.oq = now + c.oqpi
	}
	return nil
}

// Drain 返回自上次 Drain 以来时刻不大于 now 的全部本机查询，按
// （时刻，通用先于特定，组，端口）升序，并清空已发送队列。
func (c *Controller) Drain(now int64) ([]Query, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	c.pump(now)
	out := c.out
	c.out = nil
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind // General(0) 先于 GroupSpecific(1)。
		}
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Port < out[j].Port
	})
	return out, nil
}

// Forward 返回组播转发出口端口（升序、新分配切片），并推进时钟。
func (c *Controller) Forward(group uint32, inPort int, now int64) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !member.IsMulticastGroup(group) || !c.members.ValidPort(inPort) {
		return nil, member.ErrInvalidParam
	}
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	c.pump(now)

	routers := c.routerPortsExcept(-1)
	members := c.members.Members(group)
	return forward.Decide(c.p, group, inPort, members, routers, c.flood), nil
}

// IsQuerier 报告本机当前是否为查询器。
func (c *Controller) IsQuerier() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isQuerier
}

// Touched 返回最近一次 Forward 中成员表触碰的本组成员记录数（不含堆到期与路由器端口）。
func (c *Controller) Touched() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.members.Touched()
}
