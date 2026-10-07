package bitemporal

import "fmt"

// MigratedRecord 是迁移后的记录。
type MigratedRecord struct {
	Record  *Record
	Verdict Verdict
}

// Migrate 按判定结果执行单步迁移。
//
// 迁移不修改输入记录，而是构造并返回一条新记录：
//   - incompatible / record_invalid / unknown_version：不产出记录
//     （Record 为 nil），仅返回判定结果；
//   - info_loss / compatible：按目标版本转换保留轴的边界约定，
//     丢弃目标不记录的轴，并对“目标记录而源不记录”的轴按
//     DefaultInterval 的唯一固定规则填充，不遗留未定义状态。
func Migrate(reg *Registry, r *Record, src, dst string) MigratedRecord {
	v := Judge(reg, r, src, dst)
	if v.Status == StatusIncompatible || v.Status == StatusRecordInvalid || v.Status == StatusUnknownVersion {
		return MigratedRecord{Record: nil, Verdict: v}
	}
	srcVer, _ := reg.Get(src)
	dstVer, _ := reg.Get(dst)
	out := &Record{ID: r.ID}
	for _, a := range allAxes {
		srcSpec := srcVer.AxisSpecOf(a)
		dstSpec := dstVer.AxisSpecOf(a)
		if !dstSpec.Recorded {
			continue
		}
		if srcSpec.Recorded {
			converted, ok := ConvertInterval(*axisInterval(r, a), dstSpec)
			if !ok {
				// 理论上不可达：Judge 已在优先级 2 拦截。保守处理为不兼容。
				v.Status = StatusIncompatible
				v.Reasons = append(v.Reasons, fmt.Sprintf("%s: interval conversion failed", a))
				return MigratedRecord{Record: nil, Verdict: v}
			}
			putAxis(out, a, &converted)
		} else {
			fill := DefaultInterval(dstSpec)
			putAxis(out, a, &fill)
		}
	}
	return MigratedRecord{Record: out, Verdict: v}
}

// MigratePath 沿一条版本路径连续迁移（path 为包含起点与终点的版本序列）。
//
// 路径组合遵循两条规则：
//
//  1. 已丢失的轴不恢复：某跳丢弃一条轴后，后续跳即使进入记录该轴的
//     版本，也只能得到按固定规则填充的永恒区间，而不会恢复原始值。
//  2. 损失按路径累计并去重：一条轴在整条路径上只要被任一中间版本丢弃，
//     最终 Verdict 就报告该轴损失一次。
//
// 任一跳出现硬错误（记录不自洽 / 边界不兼容 / 版本不可识别）即整体失败。
// 在路径终点版本与直迁目标版本相同、且不经过更弱中间版本的常见情形下，
// 路径迁移与直接迁移的双时态信息及损失报告完全一致（见 TestPathEquivalence）。
func MigratePath(reg *Registry, r *Record, path []string) MigratedRecord {
	if len(path) < 2 {
		v := Verdict{
			RecordID: r.ID,
			Status:   StatusUnknownVersion,
			Reasons:  []string{"migration path must contain at least two versions"},
		}
		return MigratedRecord{Record: nil, Verdict: v}
	}

	cur := r
	var hopVerdicts []Verdict
	for i := 0; i+1 < len(path); i++ {
		m := Migrate(reg, cur, path[i], path[i+1])
		hopVerdicts = append(hopVerdicts, m.Verdict)
		if m.Record == nil {
			return MigratedRecord{Record: nil, Verdict: m.Verdict}
		}
		cur = m.Record
	}

	merged := Verdict{RecordID: r.ID}
	lost := map[Axis]bool{}
	filled := map[Axis]bool{}
	for _, hv := range hopVerdicts {
		for _, l := range hv.Lost {
			lost[l.Axis] = true
		}
		for _, f := range hv.Filled {
			filled[f.Axis] = true
		}
		merged.Boundary = append(merged.Boundary, hv.Boundary...)
	}
	dstVer, _ := reg.Get(path[len(path)-1])
	for _, a := range allAxes {
		if lost[a] {
			merged.Lost = append(merged.Lost, LostAxis{
				Axis:   a,
				Reason: fmt.Sprintf("axis dropped along path %v", path),
			})
			continue
		}
		if filled[a] && dstVer.AxisSpecOf(a).Recorded {
			merged.Filled = append(merged.Filled, FilledAxis{
				Axis:   a,
				Reason: defaultFillRule(a),
			})
		}
	}
	if len(merged.Lost) > 0 {
		merged.Status = StatusInfoLoss
	} else {
		merged.Status = StatusCompatible
	}
	for _, l := range merged.Lost {
		merged.Reasons = append(merged.Reasons, string(l.Axis)+": "+l.Reason)
	}
	for _, f := range merged.Filled {
		merged.Reasons = append(merged.Reasons, string(f.Axis)+": "+f.Reason)
	}
	return MigratedRecord{Record: cur, Verdict: merged}
}

func putAxis(r *Record, a Axis, iv *Interval) {
	switch a {
	case ValidTime:
		r.Valid = iv
	case TransactionTime:
		r.Transaction = iv
	}
}
