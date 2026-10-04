// Package line 负责计划序列上的时刻推算。
package line

// Order 是计划序列中的一张工单，时刻字段由 Line 维护。
type Order struct {
	ID       string
	Family   string
	Duration int
	Due      int
	Ready    int
	Seq      int // 接受序号

	Earliest int // 接受时固定的最早开工时刻
	Start    int
	End      int
}

// ChangeoverFunc 返回族 a 到族 b 的换型时长。
type ChangeoverFunc func(a, b string) int

// Line 是一条产线的计划序列。
type Line struct {
	t0     int
	f0     string
	orders []*Order
	c      ChangeoverFunc
}

// New 创建计划序列，t0 为产线可用时刻，f0 为初始产品族。
func New(t0 int, f0 string, c ChangeoverFunc) *Line {
	return &Line{t0: t0, f0: f0, c: c}
}

// Len 返回序列长度。
func (l *Line) Len() int { return len(l.orders) }

// At 返回下标 i 处的工单指针。
func (l *Line) At(i int) *Order { return l.orders[i] }

// All 返回全部工单（按序列次序）。
func (l *Line) All() []*Order { return l.orders }

// InsertAt 在位置 pos 插入工单，不重算时刻。
func (l *Line) InsertAt(pos int, o *Order) {
	l.orders = append(l.orders, nil)
	copy(l.orders[pos+1:], l.orders[pos:])
	l.orders[pos] = o
}

// RemoveAt 删除位置 pos 的工单，不重算时刻。
func (l *Line) RemoveAt(pos int) {
	copy(l.orders[pos:], l.orders[pos+1:])
	l.orders = l.orders[:len(l.orders)-1]
}

// IndexOf 按工单号查找下标，不存在返回 -1。
func (l *Line) IndexOf(id string) int {
	for i, o := range l.orders {
		if o.ID == id {
			return i
		}
	}
	return -1
}

// RecalcFrom 从下标 k 起重算 start/end，返回重算工单数。
// start_i = max(prevEnd + C[prevFam][fam_i], earliest_i)，end_i = start_i + dur_i；
// k 之前工单的前驱取 (t0, f0) 或前一张工单。
func (l *Line) RecalcFrom(k int) int {
	if k < 0 {
		k = 0
	}
	if k >= len(l.orders) {
		return 0
	}
	var prevEnd int
	var prevFam string
	if k == 0 {
		prevEnd = l.t0
		prevFam = l.f0
	} else {
		prev := l.orders[k-1]
		prevEnd = prev.End
		prevFam = prev.Family
	}
	for i := k; i < len(l.orders); i++ {
		o := l.orders[i]
		start := prevEnd + l.c(prevFam, o.Family)
		if o.Earliest > start {
			start = o.Earliest
		}
		o.Start = start
		o.End = start + o.Duration
		prevEnd = o.End
		prevFam = o.Family
	}
	return len(l.orders) - k
}

// Clone 深拷贝序列（共享 ChangeoverFunc）。
func (l *Line) Clone() *Line {
	cp := make([]*Order, len(l.orders))
	for i, o := range l.orders {
		oc := *o
		cp[i] = &oc
	}
	return &Line{t0: l.t0, f0: l.f0, orders: cp, c: l.c}
}
