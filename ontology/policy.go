package ontology

import (
	"container/heap"
	"sync"
)

// member 是保单项下一个成员的运行时状态。
type member struct {
	annualBase    int64
	lifetimeLimit int64
	lifetimeUsed  int64
	annualUsed    map[int64]int64 // 保单年度 -> 个人年度已用
	endorse       endorsements    // 个人年度限额批改
}

func (m *member) capped() bool { return m.lifetimeUsed >= m.lifetimeLimit }

// item 是一个赔付项目的运行时状态。项目限额为个人年度项目限额，
// 已用额按 (成员, 年度) 分别记账。
type item struct {
	annualBase int64
	used       map[memberYear]int64
	endorse    endorsements
}

type memberYear struct {
	memberID string
	year     int64
}

// settledLine 记录一条已结算明细的落账结果，供冲正精确退回。
type settledLine struct {
	itemID string
	day    int64
	year   int64
	paid   int64
}

type claim struct {
	id       string
	memberID string
	lines    []settledLine
}

// Policy 持有一张保单的全部账本状态。所有变更操作在 p.mu 下串行化，
// 因此并发理赔的结果必然等价于某个串行顺序。
type Policy struct {
	mu         sync.Mutex
	id         string
	startDay   int64
	yearLength int64

	familyBase   int64
	familyUsed   map[int64]int64 // 保单年度 -> 家庭年度已用
	familyEndors endorsements

	members map[string]*member
	items   map[string]*item

	claims       map[string]*claim   // 当前有效（未冲正）理赔
	memberClaims map[string][]string // 成员 -> 受理次序的理赔号栈
	days         *dayTracker         // 有效理赔发生日的多重集合

	ops int64 // 工作量计数器：仅随本笔明细数增长，用于性能证明
}

// yearOf 返回发生日所属保单年度，仅在 day >= startDay 时调用。
func (p *Policy) yearOf(day int64) int64 {
	return (day - p.startDay) / p.yearLength
}

func newPolicy(in PolicyInput) (*Policy, *Error) {
	if in.ID == "" || in.StartDay < 0 || in.YearLength <= 0 || in.FamilyAnnualLimit < 0 {
		return nil, errOf(ErrInvalidParam, in.ID, "保单参数非法")
	}
	p := &Policy{
		id:           in.ID,
		startDay:     in.StartDay,
		yearLength:   in.YearLength,
		familyBase:   in.FamilyAnnualLimit,
		familyUsed:   make(map[int64]int64),
		members:      make(map[string]*member, len(in.Members)),
		items:        make(map[string]*item, len(in.Items)),
		claims:       make(map[string]*claim),
		memberClaims: make(map[string][]string, len(in.Members)),
		days:         newDayTracker(),
	}
	for _, mi := range in.Members {
		if mi.ID == "" || mi.AnnualLimit < 0 || mi.LifetimeLimit < 0 {
			return nil, errOf(ErrInvalidParam, in.ID, "成员参数非法")
		}
		if _, dup := p.members[mi.ID]; dup {
			return nil, errOf(ErrMemberDuplicate, in.ID, mi.ID)
		}
		p.members[mi.ID] = &member{
			annualBase:    mi.AnnualLimit,
			lifetimeLimit: mi.LifetimeLimit,
			annualUsed:    make(map[int64]int64),
		}
	}
	for _, ii := range in.Items {
		if ii.ID == "" || ii.AnnualLimit < 0 {
			return nil, errOf(ErrInvalidParam, in.ID, "项目参数非法")
		}
		if _, dup := p.items[ii.ID]; dup {
			return nil, errOf(ErrItemDuplicate, in.ID, ii.ID)
		}
		p.items[ii.ID] = &item{
			annualBase: ii.AnnualLimit,
			used:       make(map[memberYear]int64),
		}
	}
	return p, nil
}

// remaining 返回 limit - used，下限为零（批改降低限额后不追索）。
func remaining(limit, used int64) int64 {
	if r := limit - used; r > 0 {
		return r
	}
	return 0
}

// snapshot 返回指定成员、项目、年度的各层剩余额。调用方须持有 p.mu。
func (p *Policy) snapshot(memberID, itemID string, year int64) (Snapshot, *Error) {
	m, ok := p.members[memberID]
	if !ok {
		return Snapshot{}, errOf(ErrMemberNotFound, p.id, memberID)
	}
	it, ok := p.items[itemID]
	if !ok {
		return Snapshot{}, errOf(ErrItemNotFound, p.id, itemID)
	}
	return Snapshot{
		MemberAnnualRemaining: remaining(m.endorse.get(year, m.annualBase), m.annualUsed[year]),
		ItemAnnualRemaining:   remaining(it.endorse.get(year, it.annualBase), it.used[memberYear{memberID, year}]),
		FamilyAnnualRemaining: remaining(p.familyEndors.get(year, p.familyBase), p.familyUsed[year]),
		LifetimeRemaining:     remaining(m.lifetimeLimit, m.lifetimeUsed),
		Capped:                m.capped(),
	}, nil
}

// dayTracker 维护有效理赔发生日的多重集合，支持 O(log n) 取最大值。
type dayTracker struct {
	counts map[int64]int
	h      dayHeap
}

func newDayTracker() *dayTracker {
	return &dayTracker{counts: make(map[int64]int)}
}

func (d *dayTracker) add(day int64) {
	if d.counts[day] == 0 {
		heap.Push(&d.h, day)
	}
	d.counts[day]++
}

func (d *dayTracker) remove(day int64) {
	d.counts[day]--
}

// max 返回当前最大发生日；ok 为 false 表示没有有效理赔。
func (d *dayTracker) max() (int64, bool) {
	for len(d.h) > 0 && d.counts[d.h[0]] == 0 {
		heap.Pop(&d.h)
	}
	if len(d.h) == 0 {
		return 0, false
	}
	return d.h[0], true
}

type dayHeap []int64

func (h dayHeap) Len() int            { return len(h) }
func (h dayHeap) Less(i, j int) bool  { return h[i] > h[j] } // 大顶堆
func (h dayHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *dayHeap) Push(x interface{}) { *h = append(*h, x.(int64)) }
func (h *dayHeap) Pop() interface{} {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
