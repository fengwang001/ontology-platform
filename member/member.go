// Package member 维护按 (组,端口) 索引的组成员关系、到期时刻与数量限额。
//
// 所有到期都由调用方在操作入口调用 Advance 惰性落地：成员记录带版本号进入最小堆，
// 弹堆时按身份（组、端口、exp、版本）做惰性删除，刷新或改期只产生新堆项、不更新旧项。
// 因此到期处理只触碰真正到期的记录，不随组总数做全表扫描。
package member

import (
	"container/heap"
	"errors"
	"sort"
)

var (
	// ErrInvalidParam 参数非法，拒绝次序中最先判定。
	ErrInvalidParam = errors.New("member: invalid parameter")
	// ErrClockRollback now 小于已接受的最大时刻。
	ErrClockRollback = errors.New("member: clock rollback")
	// ErrLocalGroup 目的组为 0xE0000000..0xE00000FF 本地链路组。
	ErrLocalGroup = errors.New("member: link-local multicast group")
	// ErrNotMember 该 (组,端口) 不是未到期成员。
	ErrNotMember = errors.New("member: not a member")
	// ErrPortLimit 该端口未到期成员关系数已达每端口上限。
	ErrPortLimit = errors.New("member: per-port membership limit reached")
	// ErrGroupLimit 有成员的组数已达全机上限。
	ErrGroupLimit = errors.New("member: group count limit reached")
)

const minGroup = uint32(0xE0000000)
const maxGroup = uint32(0xEFFFFFFF)
const maxLocalGroup = uint32(0xE00000FF)

// Rec 描述一条成员关系的可见状态。
type Rec struct {
	Group   uint32
	Port    int
	Exp     int64
	Pending bool // 是否处于末成员查询等待（已降低 exp、待特定组查询完成）
}

type rec struct {
	group   uint32
	port    int
	exp     int64
	ver     int64
	pending bool
}

type heapItem struct {
	group uint32
	port  int
	exp   int64
	ver   int64
}

type minHeap []heapItem

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].exp < h[j].exp }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(heapItem)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Table 是成员关系表。
type Table struct {
	p        int
	gmi      int64
	gmax     int
	lp       int
	clock    int64
	byGroup  map[uint32]map[int]*rec
	portCnt  []int
	groupN   int
	deadline minHeap
	// touched 仅累计最近一次 Members 触碰的现存成员记录数，不含堆操作。
	touched int
}

// New 构造成员关系表。
func New(P int, gmi int64, gmax, lp int) *Table {
	return &Table{
		p:        P,
		gmi:      gmi,
		gmax:     gmax,
		lp:       lp,
		byGroup:  make(map[uint32]map[int]*rec),
		portCnt:  make([]int, P+1),
		deadline: make(minHeap, 0),
	}
}

// Clock 返回已接受的最大时刻。
func (t *Table) Clock() int64 { return t.clock }

// IsMulticastGroup 判断组是否落在 0xE0000000..0xEFFFFFFF。
func IsMulticastGroup(group uint32) bool { return group >= minGroup && group <= maxGroup }

// IsLocalGroup 判断组是否为本地链路组（0xE0000000..0xE00000FF）。
func IsLocalGroup(group uint32) bool {
	return group >= minGroup && group <= maxLocalGroup
}

// ValidPort 判断端口号是否属于 [1,P]。
func (t *Table) ValidPort(port int) bool { return port >= 1 && port <= t.p }

// Advance 把所有 exp<=now 的成员记录落地删除，随后推进时钟。
func (t *Table) Advance(now int64) {
	for len(t.deadline) > 0 && t.deadline[0].exp <= now {
		t.PopExpiry()
	}
	t.clock = now
}

// SetClock 在外部已交错完成到期弹出后仅推进成员表时钟。
func (t *Table) SetClock(now int64) { t.clock = now }

// NextExpiry 返回下一个“仍有效”的到期堆项时刻；堆顶为陈旧项时就地丢弃。
// 第二返回值为假表示没有任何待到期记录。
func (t *Table) NextExpiry() (int64, bool) {
	for len(t.deadline) > 0 {
		it := t.deadline[0]
		ports := t.byGroup[it.group]
		r := ports[it.port]
		if r == nil || r.exp != it.exp || r.ver != it.ver {
			heap.Pop(&t.deadline)
			continue
		}
		return it.exp, true
	}
	return 0, false
}

// PopExpiry 弹出堆顶项：仍有效则删除该成员记录，陈旧则直接丢弃。
func (t *Table) PopExpiry() {
	it := heap.Pop(&t.deadline).(heapItem)
	ports := t.byGroup[it.group]
	if ports == nil {
		return
	}
	r := ports[it.port]
	if r == nil || r.exp != it.exp || r.ver != it.ver {
		return
	}
	t.deleteRec(it.group, it.port)
}

func (t *Table) deleteRec(group uint32, port int) {
	ports := t.byGroup[group]
	if ports == nil || ports[port] == nil {
		return
	}
	delete(ports, port)
	t.portCnt[port]--
	if len(ports) == 0 {
		delete(t.byGroup, group)
		t.groupN--
	}
}

func (t *Table) pushDeadline(r *rec) {
	heap.Push(&t.deadline, heapItem{group: r.group, port: r.port, exp: r.exp, ver: r.ver})
}

// ClearPending 清除全部成员记录的末成员查询等待标记，但不改变其到期时刻。
// 供本机让位时使用：未发完的特定组查询被取消，而已降低的 exp 不恢复。
func (t *Table) ClearPending() {
	for _, ports := range t.byGroup {
		for _, r := range ports {
			r.pending = false
		}
	}
}

// Report 建立或刷新 (group,port) 成员关系。
// 返回该成员此前是否处于末成员查询等待（存在尚未发完的特定组查询）。
// 调用方须先完成参数、时钟、本地链路组检查并调用 Advance。
func (t *Table) Report(port int, group uint32, now int64) (wasPending bool, err error) {
	exp := now + t.gmi
	if ports := t.byGroup[group]; ports != nil {
		if r := ports[port]; r != nil {
			wasPending = r.pending
			r.ver++
			r.exp = exp
			r.pending = false
			t.pushDeadline(r)
			return wasPending, nil
		}
	}
	if t.portCnt[port] >= t.lp {
		return false, ErrPortLimit
	}
	if _, exists := t.byGroup[group]; !exists && t.groupN >= t.gmax {
		return false, ErrGroupLimit
	}
	r := &rec{group: group, port: port, exp: exp}
	if t.byGroup[group] == nil {
		t.byGroup[group] = make(map[int]*rec)
	}
	t.byGroup[group][port] = r
	t.portCnt[port]++
	t.groupN = len(t.byGroup)
	t.pushDeadline(r)
	return false, nil
}

// FastLeave 立即移除成员关系，非成员返回 ErrNotMember。
// 调用方须先完成参数、时钟、本地链路组检查并调用 Advance。
func (t *Table) FastLeave(port int, group uint32) error {
	ports := t.byGroup[group]
	if ports == nil || ports[port] == nil {
		return ErrNotMember
	}
	t.deleteRec(group, port)
	return nil
}

// PrepareLeave 处理非快速离组：返回当前成员记录副本与“是否已有未发完特定组查询”。
// 当 ready 为真时，调用方应把到期时刻只降不升地改为 exp，并安排 Rb 条特定组查询；
// alreadyPending 为真时调用方整体忽略本次 Leave（不重排、不改 exp）。
// 调用方须先完成参数、时钟、本地链路组检查并调用 Advance。
func (t *Table) PrepareLeave(port int, group uint32, exp int64) (r Rec, alreadyPending bool, err error) {
	ports := t.byGroup[group]
	if ports == nil || ports[port] == nil {
		return Rec{}, false, ErrNotMember
	}
	cur := ports[port]
	if cur.pending {
		return Rec{Group: group, Port: port, Exp: cur.exp, Pending: true}, true, nil
	}
	cur.pending = true
	if exp < cur.exp {
		cur.ver++
		cur.exp = exp
		t.pushDeadline(cur)
	}
	return Rec{Group: group, Port: port, Exp: cur.exp, Pending: true}, false, nil
}

// Lookup 返回成员记录（Pending 为其末成员查询等待标记）；非成员时 ok 为假。
func (t *Table) Lookup(port int, group uint32) (Rec, bool) {
	ports := t.byGroup[group]
	if ports == nil || ports[port] == nil {
		return Rec{}, false
	}
	r := ports[port]
	return Rec{Group: r.group, Port: r.port, Exp: r.exp, Pending: r.pending}, true
}

// Members 返回某组当前未到期的成员端口（升序、新分配切片），并把 touched 置为本次
// 触碰的现存成员记录数（仅本组成员记录）。调用前须已 Advance(now)。
func (t *Table) Members(group uint32) []int {
	ports := t.byGroup[group]
	t.touched = len(ports)
	if len(ports) == 0 {
		return []int{}
	}
	out := make([]int, 0, len(ports))
	for p := range ports {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// Touched 返回最近一次 Members 触碰的成员记录数。
func (t *Table) Touched() int { return t.touched }

// GroupCount 返回当前有成员的组数。
func (t *Table) GroupCount() int { return t.groupN }

// PortCount 返回某端口当前未到期成员关系数。
func (t *Table) PortCount(port int) int { return t.portCnt[port] }
