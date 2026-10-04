package equip

import (
	"errors"
	"sort"
)

var (
	ErrInvalid   = errors.New("equip: invalid argument")
	ErrDuplicate = errors.New("equip: duplicate equipment type")
)

type timedEvent struct {
	at    int64
	delta int
	end   bool
}

// sortEvents 按时刻升序；同一时刻结束事件排在开始事件之前，
// 保证 [a,b) 与 [b,c) 不被算作同时占用。
func sortEvents(events []timedEvent) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		if events[i].end != events[j].end {
			return events[i].end
		}
		return events[i].delta < events[j].delta
	})
}

// Pool 保存各类设备的总数与消毒时长。
type Pool struct {
	total map[string]int
	st    map[string]int
}

func NewPool() *Pool {
	return &Pool{total: map[string]int{}, st: map[string]int{}}
}

// Add 登记一类设备：类型名非空，n 取值 [1,100]，st 取值 [0,240]，类型不重复。
func (p *Pool) Add(typeName string, n, st int) error {
	if typeName == "" || n < 1 || n > 100 || st < 0 || st > 240 {
		return ErrInvalid
	}
	if _, ok := p.total[typeName]; ok {
		return ErrDuplicate
	}
	p.total[typeName] = n
	p.st[typeName] = st
	return nil
}

// Has 判断设备类型是否已登记。
func (p *Pool) Has(typeName string) bool {
	_, ok := p.total[typeName]
	return ok
}

// Total 返回某类型的数量；未登记返回 0。
func (p *Pool) Total(typeName string) int { return p.total[typeName] }

// ST 返回某类型的消毒时长；未登记返回 0。
func (p *Pool) ST(typeName string) int { return p.st[typeName] }

// OccupyEnd 返回占用结束时刻 end+st。
func (p *Pool) OccupyEnd(typeName string, end int64) int64 {
	return end + int64(p.st[typeName])
}

// Types 返回所有已登记类型，字节序升序。
func (p *Pool) Types() []string {
	names := make([]string, 0, len(p.total))
	for name := range p.total {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Use 为一台手术对设备的占用：区间 [Start, End) 为手术区间，
// 实际占用 [Start, End+st)，各类型数量见 Quantities。
type Use struct {
	ID         string
	Start      int64
	End        int64
	Quantities map[string]int
}

// Peak 返回在已给占用 uses 之上再叠加 add（uses 与 add 均给出手术区间，
// 函数内部按 st 延长右端）后，类型 typeName 的最大并发占用件数。
// uses 自身满足容量约束，故峰值只会出现在 add 的占用区间内。
// 端点事件扫描：同一时刻先处理结束再处理开始，恰等到点相容。
func (p *Pool) Peak(typeName string, uses []Use, add *Use) int {
	st := int64(p.st[typeName])
	var events []timedEvent
	for _, u := range uses {
		q := u.Quantities[typeName]
		if q <= 0 {
			continue
		}
		s, e := u.Start, u.End+st
		if s >= e {
			continue
		}
		events = append(events, timedEvent{at: s, delta: q}, timedEvent{at: e, delta: -q, end: true})
	}
	if add != nil {
		addQty := add.Quantities[typeName]
		s, e := add.Start, add.End+st
		if addQty > 0 && s < e {
			events = append(events, timedEvent{at: s, delta: addQty}, timedEvent{at: e, delta: -addQty, end: true})
		}
	}
	if len(events) == 0 {
		return 0
	}
	sortEvents(events)
	cur, peak := 0, 0
	for _, e := range events {
		// 同一点先减后增由排序保证；恰等相容。
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak
}

// Shortage 判断在 uses 基础上加入 add 后，指定类型是否超出数量。
// 返回不足的件数（0 表示充足）。
func (p *Pool) Shortage(typeName string, uses []Use, add *Use) int {
	peak := p.Peak(typeName, uses, add)
	if peak > p.total[typeName] {
		return peak - p.total[typeName]
	}
	return 0
}

// Occupies 判断 use 是否在 typeName 上占用且其占用区间与 [lo,hi) 相交。
func (p *Pool) Occupies(use Use, typeName string, lo, hi int64) bool {
	q := use.Quantities[typeName]
	if q <= 0 {
		return false
	}
	s, e := use.Start, use.End+int64(p.st[typeName])
	return s < hi && lo < e
}
