// Package plan 提供计数抽样方案表：按批量区间与严格度档位查询 (n, Ac, Re)。
package plan

import (
	"errors"
	"sort"
)

// Severity 为检验严格度档位。Suspended 不是抽样档，仅表示检验流已暂停。
type Severity int

const (
	Normal Severity = iota
	Tightened
	Reduced
	Suspended
)

func (s Severity) String() string {
	switch s {
	case Normal:
		return "Normal"
	case Tightened:
		return "Tightened"
	case Reduced:
		return "Reduced"
	case Suspended:
		return "Suspended"
	}
	return "Unknown"
}

// Plan 为一组计数抽样方案：样本量 N、接收数 Ac、拒收数 Re。
type Plan struct {
	N  int
	Ac int
	Re int
}

// Range 为一个批量闭区间 [Lo, Hi] 及其正常、加严、放宽三档方案。
type Range struct {
	Lo, Hi    int
	Normal    Plan
	Tightened Plan
	Reduced   Plan
}

// ErrInvalidParam 表示方案表构造参数非法。
var ErrInvalidParam = errors.New("plan: 参数非法")

// Table 为抽样方案表，区间按 Lo 升序保存，互不重叠。
type Table struct {
	ranges []Range
	lr     int
}

// NewTable 构造方案表。要求：区间非空、[lo,hi] 闭区间且互不重叠；
// 每组方案 n>=1、0<=Ac<Re；Normal 与 Tightened 须 Re=Ac+1；
// Reduced 允许 Re>Ac+1；放宽限数 lr 非负。违反者返回 ErrInvalidParam。
func NewTable(ranges []Range, lr int) (*Table, error) {
	if lr < 0 || len(ranges) == 0 {
		return nil, ErrInvalidParam
	}
	rs := make([]Range, len(ranges))
	copy(rs, ranges)
	for _, r := range rs {
		if r.Lo < 1 || r.Lo > r.Hi {
			return nil, ErrInvalidParam
		}
		if err := checkPlan(r.Normal, true); err != nil {
			return nil, err
		}
		if err := checkPlan(r.Tightened, true); err != nil {
			return nil, err
		}
		if err := checkPlan(r.Reduced, false); err != nil {
			return nil, err
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Lo < rs[j].Lo })
	for i := 1; i < len(rs); i++ {
		if rs[i].Lo <= rs[i-1].Hi {
			return nil, ErrInvalidParam
		}
	}
	return &Table{ranges: rs, lr: lr}, nil
}

func checkPlan(p Plan, unitInterval bool) error {
	if p.N < 1 || p.Ac < 0 || p.Ac >= p.Re {
		return ErrInvalidParam
	}
	if unitInterval && p.Re != p.Ac+1 {
		return ErrInvalidParam
	}
	return nil
}

// Lookup 返回覆盖批量 n 的区间；不存在时 ok 为 false。
func (t *Table) Lookup(n int) (r Range, ok bool) {
	i := sort.Search(len(t.ranges), func(i int) bool { return t.ranges[i].Hi >= n })
	if i < len(t.ranges) && t.ranges[i].Lo <= n {
		return t.ranges[i], true
	}
	return Range{}, false
}

// Plan 返回批量 n 在严格度 sev 下的方案；n 无区间或 sev 非抽样档时 ok 为 false。
func (t *Table) Plan(n int, sev Severity) (Plan, bool) {
	r, ok := t.Lookup(n)
	if !ok {
		return Plan{}, false
	}
	switch sev {
	case Normal:
		return r.Normal, true
	case Tightened:
		return r.Tightened, true
	case Reduced:
		return r.Reduced, true
	}
	return Plan{}, false
}

// Lr 返回放宽限数。
func (t *Table) Lr() int { return t.lr }
