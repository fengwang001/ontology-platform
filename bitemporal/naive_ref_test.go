package bitemporal

// naive_reference_model.go（仅测试使用）：
//
// 朴素参照模型——刻意用与生产实现不同的、最直白的方式独立推导结论：
//   - 区间归属：逐个枚举候选时点，按端点开闭做朴素比较；
//   - 边界差异：枚举源区间端点邻域的时点集合，逐个时点比较
//     “源格式是否包含”与“目标格式下任何候选表示能否保持相同归属”；
//   - 版本规则：直接按 4 个 if 层级逐步独立判定，不共享生产代码路径。
//
// 随机对照测试用大量随机场景验证生产实现与该参照模型结论一致，
// 并记录每次判定的输入、输出与依据。

import (
	"fmt"
	"sort"
)

// naiveContains 用最直白的比较实现时点归属。
func naiveContains(iv Interval, t int64) bool {
	leftOK := t >= iv.Start // 本模型起点恒包含
	var rightOK bool
	if iv.EndBound == Closed {
		rightOK = t <= iv.End
	} else {
		rightOK = t < iv.End
	}
	return leftOK && rightOK
}

// naiveMembershipDiffers 枚举端点邻域时点，判断是否存在归属改变。
// 返回 (是否不同, witness)。
func naiveMembershipDiffers(iv Interval, dst AxisSpec) (bool, int64) {
	if iv.Empty() && dst.EndBound == Closed {
		return true, iv.Start // 空集无法写成闭区间
	}
	last := iv.End
	if iv.EndBound == HalfOpen {
		last = iv.End - 1
	}
	if dst.EndBound == HalfOpen && last >= MaxFinite {
		return true, MaxFinite
	}
	// 枚举候选时点：起点前后、终点前后。
	for _, t := range []int64{iv.Start - 1, iv.Start, last, last + 1} {
		if t < MinFinite || t > MaxFinite {
			continue
		}
		// 生产转换后应有的归属
		var conv Interval
		if dst.EndBound == Closed {
			conv = Interval{Start: iv.Start, End: last, StartBound: Closed, EndBound: Closed}
		} else {
			conv = Interval{Start: iv.Start, End: last + 1, StartBound: Closed, EndBound: HalfOpen}
		}
		if naiveContains(iv, t) != naiveContains(conv, t) {
			return true, t
		}
	}
	return false, 0
}

// naiveJudge 是按题面规则逐步独立判定的参照实现。
// 返回 (状态, 丢失的轴, 填充的轴, 依据说明)。
func naiveJudge(reg *Registry, r *Record, src, dst string) (Status, []Axis, []Axis, []string) {
	var reasons []string

	// 规则 1：记录自身区间不自洽（起点晚于终点等）。
	check := func(a Axis, iv *Interval) bool {
		if iv == nil {
			return true
		}
		if iv.Start > iv.End {
			reasons = append(reasons, fmt.Sprintf("%s start>end", a))
			return false
		}
		if iv.Start < MinFinite || iv.End > MaxFinite {
			reasons = append(reasons, fmt.Sprintf("%s out of domain", a))
			return false
		}
		return true
	}
	if !check(ValidTime, r.Valid) || !check(TransactionTime, r.Transaction) {
		return StatusRecordInvalid, nil, nil, reasons
	}

	// 规则 4（查表）：版本可识别性。
	srcVer, sOK := reg.Get(src)
	dstVer, dOK := reg.Get(dst)
	if !sOK || !dOK {
		reasons = append(reasons, "unknown version")
		return StatusUnknownVersion, nil, nil, reasons
	}

	// 规则 2：共享轴上的边界语义差异是否改变归属。
	for _, a := range []Axis{ValidTime, TransactionTime} {
		ss := srcVer.AxisSpecOf(a)
		ds := dstVer.AxisSpecOf(a)
		if !ss.Recorded || !ds.Recorded {
			continue
		}
		if ss.EndBound != ds.EndBound {
			iv := axisInterval(r, a)
			if diff, _ := naiveMembershipDiffers(*iv, ds); diff {
				reasons = append(reasons, fmt.Sprintf("%s boundary membership differs", a))
				return StatusIncompatible, nil, nil, reasons
			}
		}
	}

	// 规则 3：轴记录与否的差异。
	var lost, filled []Axis
	for _, a := range []Axis{ValidTime, TransactionTime} {
		ss := srcVer.AxisSpecOf(a)
		ds := dstVer.AxisSpecOf(a)
		switch {
		case ss.Recorded && !ds.Recorded:
			lost = append(lost, a)
		case !ss.Recorded && ds.Recorded:
			filled = append(filled, a)
		}
	}
	sort.Slice(lost, func(i, j int) bool { return lost[i] < lost[j] })
	sort.Slice(filled, func(i, j int) bool { return filled[i] < filled[j] })
	if len(lost) > 0 {
		return StatusInfoLoss, lost, filled, reasons
	}
	return StatusCompatible, nil, filled, reasons
}
