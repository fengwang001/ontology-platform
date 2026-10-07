package bitemporal

import "fmt"

// BoundaryVerdict 描述源/目标边界语义差异对查询时点归属的影响。
type BoundaryVerdict struct {
	Axis       Axis
	Differs    bool
	Witness    int64
	HasWitness bool
	Reason     string
}

// CompareBoundary 比较同一时间轴上的边界语义差异。
//
// 判定方法（离散整数时点域）：区间由它实际包含的整数时点集合唯一确定。
// 若能在目标边界约定下表示完全相同的点集，则边界语义差异不改变任何
// 查询时点的归属判定（Differs=false）；若不能（例如闭区间右端到达域末
// MaxFinite 时无法在域内写成半开区间，或半开空区间无法表示为空闭区间），
// 则至少存在一个查询时点在两种格式下归属结果不同，此时 Differs=true 并
// 给出一个可复核的 witness 时点。
//
// 这是判定固定优先级的第 2 级：只要存在归属判定可能不同的边界语义差异，
// 整体判定即为“不兼容”（而非“有信息损失”）。
func CompareBoundary(axis Axis, src, dst AxisSpec, iv Interval) BoundaryVerdict {
	v := BoundaryVerdict{Axis: axis}
	if src.StartBound == dst.StartBound && src.EndBound == dst.EndBound {
		return v
	}
	converted, ok := convertPointSet(iv, dst.StartBound, dst.EndBound)
	if ok {
		// 点集可无损表示：核对端点附近所有相关时点的归属是否一致。
		for _, t := range boundaryProbePoints(iv, converted) {
			if iv.Contains(t) != converted.Contains(t) {
				v.Differs = true
				v.Witness, v.HasWitness = t, true
				v.Reason = fmt.Sprintf("membership differs at probe time %d", t)
				return v
			}
		}
		v.Reason = "boundary convention differs but the contained point set is representable without membership change"
		return v
	}
	// 无法表示相同点集：给出一个 witness 时点证明归属判定必然改变。
	w, reason, found := membershipWitness(iv, dst)
	v.Differs = true
	if found {
		v.Witness, v.HasWitness = w, true
	}
	v.Reason = reason
	return v
}

// ConvertInterval 按目标边界约定转换区间，保持所包含的整数时点集合不变。
// 转换不可能保持归属结果时 ok=false（调用方应据此判定为不兼容）。
func ConvertInterval(iv Interval, dst AxisSpec) (Interval, bool) {
	return convertPointSet(iv, dst.StartBound, dst.EndBound)
}

// convertPointSet 在离散整数时点域上把区间转换为指定的端点约定，
// 目标区间与源区间包含完全相同的整数时点。
func convertPointSet(iv Interval, dstStart, dstEnd BoundMode) (Interval, bool) {
	out := Interval{StartBound: dstStart, EndBound: dstEnd}
	first, hasFirst := iv.firstPoint()
	last, hasLast := iv.lastPoint()

	if !hasFirst || !hasLast {
		// 空集无法以“起点包含”的闭区间表示；本组件不引入全开区间，
		// 因此空集只能保持半开形式 [s, s)。
		return Interval{}, false
	}

	// 本组件的区间模型起点恒为包含；不支持半开起点的自定义版本约定。
	if dstStart != Closed {
		return Interval{}, false
	}
	// first 即起始时点。
	out.Start = first

	// 右端：闭 => 端点即最后时点；半开 => 端点为最后时点 +1。
	if dstEnd == Closed {
		out.End = last
	} else {
		if last >= MaxFinite {
			return Interval{}, false
		}
		out.End = last + 1
	}
	return out, true
}

// boundaryProbePoints 收集源区间与转换后区间端点附近需要核对归属的时点。
func boundaryProbePoints(src, dst Interval) []int64 {
	pts := map[int64]struct{}{}
	for _, iv := range []Interval{src, dst} {
		for _, e := range []int64{iv.Start, iv.End, iv.Start - 1, iv.End + 1} {
			if e >= MinFinite && e <= MaxFinite {
				pts[e] = struct{}{}
			}
		}
	}
	out := make([]int64, 0, len(pts))
	for t := range pts {
		out = append(out, t)
	}
	return out
}

// membershipWitness 在无法保持点集时，给出一个源/目标归属必然不同的时点。
func membershipWitness(src Interval, dst AxisSpec) (int64, string, bool) {
	if src.Empty() {
		// 源为空集、目标为闭端约定：任何 [s, s] 闭区间都非空，
		// 无法表达空集；取源起点作为目标会错误包含的时点。
		t := src.Start
		return t, fmt.Sprintf("empty half-open interval cannot be represented as closed; e.g. %d would be included", t), true
	}
	last, _ := src.lastPoint()
	if dst.EndBound == HalfOpen && last >= MaxFinite {
		// 闭端延伸到域末：半开表示需要域外端点 MaxFinite+1。
		return MaxFinite, fmt.Sprintf("closed end at domain max %d has no in-domain half-open representation; membership at %d changes", MaxFinite, MaxFinite), true
	}
	return 0, "target bound convention cannot represent the source point set", false
}
