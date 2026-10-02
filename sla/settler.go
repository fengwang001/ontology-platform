// Package sla 实现带排除窗口、连续违约升级与年度封顶的服务等级违约积分结算器。
package sla

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidParam     = errors.New("参数非法")
	ErrMonthOpen        = errors.New("月已开放")
	ErrNoOpenMonth      = errors.New("无开放月")
	ErrExclusionLimit   = errors.New("排除超限")
	ErrIntervalNotFound = errors.New("不存在")
)

// Params 为结算器构造参数。
type Params struct {
	L          int64 // 月长（分钟），1..1e6
	G          int64 // 抖动下限，0..L
	T1, T2, T3 int64 // 可用度档位（百万分比），T1>T2>T3，1..1e6
	C1, C2, C3 int64 // 积分百分点，C1<C2<C3，1..100
	St         int64 // 升级步长，0..100
	Sm         int64 // 升级封顶月数，0..12
	Y          int64 // 年度积分额度（分），0..1e12
	Ex         int64 // 每月排除上限（分钟），0..L
}

// Result 为 Close 的结算结果。
type Result struct {
	D      int64 // 扣除排除并丢弃抖动后的故障总时长（分钟）
	A      int64 // 可用度（百万分比），向下取整
	Base   int64 // 基础积分百分点
	Pct    int64 // 升级后的积分百分点
	Credit int64 // 本月发放积分（分）
}

type interval struct {
	a, b int64
}

// Settler 为违约积分结算器，所有方法可并发调用。
type Settler struct {
	mu       sync.Mutex
	p        Params
	month    int64
	open     bool
	fee      int64
	faults   map[interval]int64
	excludes []interval
	s        int64
	yearPaid map[int64]int64
}

// New 构造结算器，参数不满足约束时整体拒绝。
func New(p Params) (*Settler, error) {
	if err := validateParams(p); err != nil {
		return nil, err
	}
	return &Settler{
		p:        p,
		faults:   make(map[interval]int64),
		yearPaid: make(map[int64]int64),
	}, nil
}

func validateParams(p Params) error {
	if p.L < 1 || p.L > 1_000_000 {
		return ErrInvalidParam
	}
	if p.G < 0 || p.G > p.L {
		return ErrInvalidParam
	}
	if !(p.T1 > p.T2 && p.T2 > p.T3) || p.T3 < 1 || p.T1 > 1_000_000 {
		return ErrInvalidParam
	}
	if !(p.C1 < p.C2 && p.C2 < p.C3) || p.C1 < 1 || p.C3 > 100 {
		return ErrInvalidParam
	}
	if p.St < 0 || p.St > 100 {
		return ErrInvalidParam
	}
	if p.Sm < 0 || p.Sm > 12 {
		return ErrInvalidParam
	}
	if p.Y < 0 || p.Y > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if p.Ex < 0 || p.Ex > p.L {
		return ErrInvalidParam
	}
	return nil
}

func validInterval(p Params, a, b int64) bool {
	return 0 <= a && a < b && b <= p.L
}

// merge 归并半开区间，重叠与首尾相接都合并。输入会被排序。
func merge(ivs []interval) []interval {
	sort.Slice(ivs, func(i, j int) bool {
		if ivs[i].a != ivs[j].a {
			return ivs[i].a < ivs[j].a
		}
		return ivs[i].b < ivs[j].b
	})
	out := ivs[:0]
	for _, iv := range ivs {
		if n := len(out); n > 0 && iv.a <= out[n-1].b {
			if iv.b > out[n-1].b {
				out[n-1].b = iv.b
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// unionLen 返回区间集合的并集总长度。
func unionLen(ivs []interval) int64 {
	if len(ivs) == 0 {
		return 0
	}
	cp := make([]interval, len(ivs))
	copy(cp, ivs)
	merged := merge(cp)
	var total int64
	for _, iv := range merged {
		total += iv.b - iv.a
	}
	return total
}

// subtract 从已归并的 segs 中减去已归并的 cuts，返回剩余残段。
func subtract(segs, cuts []interval) []interval {
	var out []interval
	i := 0
	for _, seg := range segs {
		cur := seg.a
		for i < len(cuts) && cuts[i].b <= cur {
			i++
		}
		for j := i; j < len(cuts) && cuts[j].a < seg.b; j++ {
			if cuts[j].a > cur {
				out = append(out, interval{cur, cuts[j].a})
			}
			if cuts[j].b > cur {
				cur = cuts[j].b
			}
		}
		if cur < seg.b {
			out = append(out, interval{cur, seg.b})
		}
	}
	return out
}

// NewMonth 开启下一个月。
func (st *Settler) NewMonth(fee int64) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if fee < 1 || fee > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if st.open {
		return ErrMonthOpen
	}
	st.open = true
	st.fee = fee
	st.faults = make(map[interval]int64)
	st.excludes = nil
	return nil
}

// Report 登记故障区间 [a, b)。
func (st *Settler) Report(a, b int64) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !validInterval(st.p, a, b) {
		return ErrInvalidParam
	}
	if !st.open {
		return ErrNoOpenMonth
	}
	st.faults[interval{a, b}]++
	return nil
}

// AddExclude 登记排除窗口 [a, b)。
func (st *Settler) AddExclude(a, b int64) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !validInterval(st.p, a, b) {
		return ErrInvalidParam
	}
	if !st.open {
		return ErrNoOpenMonth
	}
	if unionLen(append(append([]interval{}, st.excludes...), interval{a, b})) > st.p.Ex {
		return ErrExclusionLimit
	}
	st.excludes = append(st.excludes, interval{a, b})
	return nil
}

// Revoke 删除一份完全相同的已登记故障区间。
func (st *Settler) Revoke(a, b int64) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !validInterval(st.p, a, b) {
		return ErrInvalidParam
	}
	if !st.open {
		return ErrNoOpenMonth
	}
	iv := interval{a, b}
	if st.faults[iv] == 0 {
		return ErrIntervalNotFound
	}
	st.faults[iv]--
	if st.faults[iv] == 0 {
		delete(st.faults, iv)
	}
	return nil
}

// Close 结算当前月。
func (st *Settler) Close() (Result, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.open {
		return Result{}, ErrNoOpenMonth
	}
	p := st.p

	faultList := make([]interval, 0, len(st.faults))
	for iv, n := range st.faults {
		for k := int64(0); k < n; k++ {
			faultList = append(faultList, iv)
		}
	}
	merged := merge(faultList)
	cuts := merge(append([]interval{}, st.excludes...))
	remaining := subtract(merged, cuts)

	var d int64
	for _, iv := range remaining {
		if iv.b-iv.a >= p.G {
			d += iv.b - iv.a
		}
	}
	a := (p.L - d) * 1_000_000 / p.L

	var base int64
	switch {
	case a >= p.T1:
		base = 0
	case a >= p.T2:
		base = p.C1
	case a >= p.T3:
		base = p.C2
	default:
		base = p.C3
	}

	res := Result{D: d, A: a, Base: base}
	year := st.month / 12
	if base == 0 {
		st.s = 0
	} else {
		esc := st.s
		if esc > p.Sm {
			esc = p.Sm
		}
		pct := base + p.St*esc
		if pct > 100 {
			pct = 100
		}
		credit := (st.fee*pct + 99) / 100
		if remain := p.Y - st.yearPaid[year]; credit > remain {
			credit = remain
		}
		st.yearPaid[year] += credit
		st.s++
		res.Pct = pct
		res.Credit = credit
	}

	st.month++
	st.open = false
	st.fee = 0
	st.faults = make(map[interval]int64)
	st.excludes = nil
	return res, nil
}

// Snapshot 为内部状态快照，用于查询与测试。
type Snapshot struct {
	Month    int64
	Open     bool
	Fee      int64
	S        int64
	YearPaid map[int64]int64
}

// State 返回当前状态快照。
func (st *Settler) State() Snapshot {
	st.mu.Lock()
	defer st.mu.Unlock()
	paid := make(map[int64]int64, len(st.yearPaid))
	for y, v := range st.yearPaid {
		paid[y] = v
	}
	return Snapshot{Month: st.month, Open: st.open, Fee: st.fee, S: st.s, YearPaid: paid}
}
