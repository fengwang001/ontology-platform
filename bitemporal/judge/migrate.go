package judge

import (
	"fmt"

	"ontology/bitemporal"
)

// Migration 是一次成功迁移（compatible / lossy）的产物。
type Migration struct {
	Result Result
	// Record 是迁移后的记录。它是全新构造的值，入参记录不会被修改。
	Record bitemporal.Record
	// LostAxes 是本跳迁移中被目标版本丢弃的轴。
	LostAxes []bitemporal.Axis
	// FilledAxes 是本跳迁移中按固定规则填充的轴及其规则标识。
	FilledAxes map[bitemporal.Axis]string
}

// Migrate 判定并执行单跳迁移。只有 VerdictCompatible 与 VerdictLossy 会产出
// 记录（ok == true）；rejected / incompatible / unknown_version 不产生任何迁移结果，
// 调用方只能依据 Result 处理错误类别。
//
// 该函数不修改入参记录，也不依赖任何可变状态，重复调用结果逐字段相等。
func (e *Engine) Migrate(rec bitemporal.Record, src, dst string) (Migration, bool) {
	r := e.Judge(rec, src, dst)
	if r.Verdict != VerdictCompatible && r.Verdict != VerdictLossy {
		return Migration{Result: r}, false
	}

	srcFmt, _ := e.reg.Lookup(src)
	dstFmt, _ := e.reg.Lookup(dst)

	out := bitemporal.Record{ID: rec.ID}
	lost := []bitemporal.Axis{}
	filled := map[bitemporal.Axis]string{}

	for _, axis := range bitemporal.Axes() {
		srcIV := axisInterval(rec, axis)
		switch {
		case !dstFmt.RecordsAxis(axis):
			// 目标不记录该轴：数据丢弃并在损失报告中具名。
			if srcFmt.RecordsAxis(axis) && srcIV != nil {
				lost = append(lost, axis)
			}
		case srcIV != nil:
			// 两端都记录且源记录携带：原样保留端点值，只把闭合位投影为目标约定。
			// 到达此处说明优先级 2 已确认该投影不改变任何查询时点的归属判定。
			putAxis(&out, axis, projectInterval(*srcIV, dstFmt.Convention(axis)))
		case axis == bitemporal.ValidTime:
			// 目标记录有效轴而源格式不记录：使用唯一固定规则填充，不留未定义状态。
			putAxis(&out, axis, entireTimeline(dstFmt.Convention(axis)))
			filled[axis] = FillRuleEntireTimeline
		}
	}

	return Migration{Result: r, Record: out, LostAxes: lost, FilledAxes: filled}, true
}

// PathMigration 是记录沿一条版本链连续迁移的结果。
type PathMigration struct {
	OK bool
	// Final 是最后一跳成功时的最终记录。
	Final bitemporal.Record
	// Hops 是每一“跳”的输入版本、输出版本与迁移报告。
	Hops []Migration
	// Failure 是首个失败跳的判定结果（此时 OK == false）。
	Failure *Result
	// LostAxes 是整条路径上累积丢失的轴集合（按 bitemporal.Axes() 顺序去重）。
	LostAxes []bitemporal.Axis
	// FilledAxes 是整条路径上被固定规则填充过的轴 -> 规则标识。
	FilledAxes map[bitemporal.Axis]string
}

// MigratePath 让记录沿 versions 给出的版本链连续迁移，例如
// {"V2","V1","V2"} 表示先降级再升级。每一跳都以跳前记录为输入重新判定，
// 因此“多经过一次中间版本造成的额外损失/额外恢复”会如实体现在 Hops 中。
func (e *Engine) MigratePath(rec bitemporal.Record, versions []string) PathMigration {
	pm := PathMigration{OK: true, FilledAxes: map[bitemporal.Axis]string{}}
	cur := rec
	for i := 0; i+1 < len(versions); i++ {
		m, ok := e.Migrate(cur, versions[i], versions[i+1])
		pm.Hops = append(pm.Hops, m)
		if !ok {
			pm.OK = false
			f := m.Result
			pm.Failure = &f
			return pm
		}
		pm.LostAxes = mergeAxes(pm.LostAxes, m.LostAxes)
		for axis, rule := range m.FilledAxes {
			// 填充规则固定不变：同一轴在任何路径上都只能由同一条规则填充。
			pm.FilledAxes[axis] = rule
		}
		cur = m.Record
	}
	pm.Final = cur
	return pm
}

// PathEquivalence 报告“沿版本链连续迁移”与“直接从链首迁移到链尾”两种方式的
// 对照结果。
type PathEquivalence struct {
	PathVersions []string
	Equivalent   bool
	// Reasons 列出每一处实质性差异（额外丢失的轴、填充轴不同、最终轴数据不同、
	// 其中一条路径无法完成）。为空表示两种方式的双时态信息与损失报告完全一致。
	Reasons []string
}

// ComparePathWithDirect 对照连续迁移与直达迁移。
//
// 设计上需要明确：当中间版本不记录某条轴时，先降级再升级会把该轴永久擦除，
// 而直达迁移保留它。这不是实现缺陷，而是“逐跳物化中间快照”这一语义的必然结果，
// 信息论上不存在同时满足“中间版本真的不记录该轴”和“升级后恢复原值”的映射。
// 因此本方法不隐藏差异：凡额外损失/额外填充/最终记录不同，都逐条列入 Reasons。
// 单调升级、单调降级以及轴集合不变的版本链，对照结果恒为等价。
func (e *Engine) ComparePathWithDirect(rec bitemporal.Record, versions []string) PathEquivalence {
	res := PathEquivalence{PathVersions: append([]string(nil), versions...)}
	if len(versions) < 2 {
		res.Equivalent = true
		return res
	}

	path := e.MigratePath(rec, versions)
	direct, directOK := e.Migrate(rec, versions[0], versions[len(versions)-1])

	if !path.OK {
		res.Equivalent = false
		if path.Failure != nil {
			res.Reasons = append(res.Reasons, fmt.Sprintf(
				"连续迁移在 %s->%s 跳失败（判定=%s），直达迁移%s",
				path.Failure.SourceVersion, path.Failure.TargetVersion,
				path.Failure.Verdict, directVerdictText(directOK, direct)))
		}
		return res
	}
	if !directOK {
		res.Equivalent = false
		res.Reasons = append(res.Reasons, "连续迁移可完成，但直达迁移判定为 "+
			string(direct.Result.Verdict))
		return res
	}

	if len(path.LostAxes) != len(direct.LostAxes) {
		res.Reasons = append(res.Reasons, fmt.Sprintf(
			"累积损失轴不同：连续路径=%v，直达=%v（多经过的中间版本造成额外损失）",
			path.LostAxes, direct.LostAxes))
	}
	if intervalPtr(path.Final, bitemporal.ValidTime) == nil &&
		intervalPtr(direct.Record, bitemporal.ValidTime) != nil ||
		intervalPtr(path.Final, bitemporal.ValidTime) != nil &&
			!intervalEqual(*intervalPtr(path.Final, bitemporal.ValidTime),
				*intervalPtr(direct.Record, bitemporal.ValidTime)) {
		res.Reasons = append(res.Reasons, "最终有效时间轴数据与直达迁移不一致")
	}
	if !intervalEqualPtr(intervalPtr(path.Final, bitemporal.TransactionTime),
		intervalPtr(direct.Record, bitemporal.TransactionTime)) {
		res.Reasons = append(res.Reasons, "最终事务时间轴数据与直达迁移不一致")
	}
	if len(path.FilledAxes) != len(direct.FilledAxes) {
		res.Reasons = append(res.Reasons, "填充轴集合与直达迁移不一致")
	}
	res.Equivalent = len(res.Reasons) == 0
	return res
}

func projectInterval(iv bitemporal.Interval, conv bitemporal.BoundaryConvention) bitemporal.Interval {
	return bitemporal.Interval{
		Start:       iv.Start,
		End:         iv.End,
		StartClosed: conv.StartClosed,
		EndClosed:   conv.EndClosed,
	}
}

func entireTimeline(conv bitemporal.BoundaryConvention) bitemporal.Interval {
	// 无界端点的闭合位没有可观察语义，统一写为 Open；目标版本对无界轴的约定
	// 只影响将来被填入有限端点的场景。
	return bitemporal.Interval{Start: nil, End: nil,
		StartClosed: bitemporal.Open, EndClosed: bitemporal.Open}
}

func putAxis(rec *bitemporal.Record, axis bitemporal.Axis, iv bitemporal.Interval) {
	switch axis {
	case bitemporal.ValidTime:
		rec.Valid = &iv
	case bitemporal.TransactionTime:
		rec.Transaction = &iv
	}
}

func intervalPtr(rec bitemporal.Record, axis bitemporal.Axis) *bitemporal.Interval {
	return axisInterval(rec, axis)
}

func mergeAxes(a, b []bitemporal.Axis) []bitemporal.Axis {
	seen := map[bitemporal.Axis]bool{}
	for _, axis := range a {
		seen[axis] = true
	}
	out := append([]bitemporal.Axis(nil), a...)
	for _, axis := range b {
		if !seen[axis] {
			seen[axis] = true
			out = append(out, axis)
		}
	}
	return out
}

func intervalEqualPtr(a, b *bitemporal.Interval) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return intervalEqual(*a, *b)
}

func intervalEqual(a, b bitemporal.Interval) bool {
	return ptrInt64Equal(a.Start, b.Start) && ptrInt64Equal(a.End, b.End)
}

func ptrInt64Equal(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func directVerdictText(ok bool, m Migration) string {
	if ok {
		return "可完成（判定=" + string(m.Result.Verdict) + "）"
	}
	return "不可完成（判定=" + string(m.Result.Verdict) + "）"
}
