package recovery

import "fmt"

// Evaluate 是“发起修复/查询请求”的纯函数入口，不依赖已构造的 Coordinator，
// 用于明确表达拒绝优先级：
//
//  1. 请求对象超出覆盖范围（最高优先级）；
//  2. 快照与日志版本不衔接；
//  3. 以上均不命中时，逐对象报告来源分类，不视为错误
//     （快照对象级损坏、动作记录损坏只体现在报告里，彼此分列）。
//
// 覆盖范围 = 快照记录涉及的全部对象 ∪ 全部“完整未损坏”动作涉及的对象。
// 损坏动作的对象集合不可信，不参与覆盖计算。
// objectIDs 为空表示请求覆盖范围内全部对象。
func Evaluate(snap Snapshot, lg Log, objectIDs []ObjectID) (*Report, error) {
	snapView := verifySnapshot(&snap)
	logView := verifyActions(&lg)

	covered := make(map[ObjectID]struct{})
	for obj := range snapView.status {
		covered[obj] = struct{}{}
	}
	for _, act := range logView.actions {
		for obj := range act.Effects {
			covered[obj] = struct{}{}
		}
	}

	if len(objectIDs) > 0 {
		var missing []ObjectID
		for _, obj := range objectIDs {
			if _, ok := covered[obj]; !ok {
				missing = append(missing, obj)
			}
		}
		if len(missing) > 0 {
			// 优先级 1：即使版本同时不衔接，也先报超范围。
			return nil, &CoverageError{ObjectIDs: missing}
		}
	}

	if snap.Version != logView.base {
		// 优先级 2。
		return nil, fmt.Errorf("%w: snapshot=%q log-base=%q",
			ErrVersionMismatch, snap.Version, logView.base)
	}

	res := newEngine().replay(snapView, logView.actions, nil)
	report := &Report{
		SnapshotVersion: snap.Version,
		LogBase:         logView.base,
		Objects:         res.objects,
		CorruptObjects:  append([]ObjectID(nil), snapView.corrupt...),
		CorruptActions:  append([]ActionID(nil), logView.corrupt...),
	}
	if len(objectIDs) == 0 {
		return report, nil
	}
	filtered := &Report{
		SnapshotVersion: report.SnapshotVersion,
		LogBase:         report.LogBase,
		Objects:         make(map[ObjectID]ObjectReport, len(objectIDs)),
	}
	for _, obj := range objectIDs {
		filtered.Objects[obj] = report.Objects[obj]
	}
	return filtered, nil
}
