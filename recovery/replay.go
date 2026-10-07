package recovery

import (
	"fmt"
	"sort"
)

// Applier 抽象状态变换，便于测试与扩展。
// apply 在已知状态 base 上叠加变更 change；start 给出动作自带起点时
// （HasStart=true），以 start 而非此前已知状态作为基准。
type Applier interface {
	Apply(base, change State) State
}

// defaultApplier 用“base|change”表示一次变换，朴素且可复算。
type defaultApplier struct{}

func (defaultApplier) Apply(base, change State) State {
	if change == "" {
		return base
	}
	return base + "|" + change
}

// engine 是原子重放引擎：无锁、纯函数式输入输出。
type engine struct {
	applier Applier
}

// replayResult 是重放输出：对象级报告 + 对象 -> 锚点下标索引。
type replayResult struct {
	objects map[ObjectID]ObjectReport
	anchors map[ObjectID]int // 对象 -> 最早使其变为已知的有效动作下标；快照已知记为 -1
}

func newEngine() *engine { return &engine{applier: defaultApplier{}} }

// knownState 是重放过程中的对象状态。
type knownState struct {
	known bool
	state State
}

// replay 依次应用全部完整动作，并把每次判定的输入/输出/依据写入 log。
//
// 动作原子性：应用任何效果之前，先检查涉及的全部对象——
// 只要存在一个对象当前状态未知且该动作没有为它自带完整起点，
// 整条动作对其涉及的全部对象整体放弃（包括其中本来已知的对象），
// 等价于该动作从未发生；后续动作不受影响。
func (e *engine) replay(view *snapshotView, actions []Action, log *[]DecisionLogEntry) *replayResult {
	res := &replayResult{
		objects: make(map[ObjectID]ObjectReport),
		anchors: make(map[ObjectID]int),
	}
	cur := make(map[ObjectID]knownState)

	// 覆盖范围 = 快照中的全部记录 + 全部完整动作涉及的对象。
	cover := make(map[ObjectID]struct{})
	for obj := range view.status {
		cover[obj] = struct{}{}
	}
	for obj := range objectsOf(actions) {
		cover[obj] = struct{}{}
	}

	// 初始状态仅来自完好快照记录；损坏记录是“不可读”，不是空状态。
	for obj := range cover {
		st := view.status[obj]
		if st == StatusValid {
			cur[obj] = knownState{known: true, state: view.state[obj]}
			res.anchors[obj] = -1
		}
	}

	for idx, act := range actions {
		applied, blocker := explainAction(act, cur)
		if !applied {
			// 原子性整体放弃：不改动任何对象的状态。
			if log != nil {
				*log = append(*log, DecisionLogEntry{
					Stage:  "action-apply",
					Input:  fmt.Sprintf("action=%s index=%d objects=%s", act.ActionID, idx, formatEffectSet(act)),
					Output: "skipped(whole action, no object updated)",
					Reason: fmt.Sprintf("object %s has unknown starting state and action carries no start for it; atomicity requires all-or-nothing", blocker),
				})
			}
			continue
		}
		for obj, ef := range act.Effects {
			if !cur[obj].known {
				// 快照不可读或此前无状态：自本动作起锚定，不向更早回溯。
				res.anchors[obj] = idx
				cur[obj] = knownState{known: true, state: e.applier.Apply(ef.Start, ef.Change)}
				if log != nil {
					*log = append(*log, DecisionLogEntry{
						Stage:  "action-apply",
						Input:  fmt.Sprintf("action=%s index=%d object=%s prior=unknown start=%q change=%q", act.ActionID, idx, obj, ef.Start, ef.Change),
						Output: "anchored-and-applied",
						Reason: "complete action supplies starting state; object is readable from this action onward, no earlier reconstruction",
					})
				}
				continue
			}
			base := cur[obj].state
			if ef.HasStart {
				base = ef.Start
			}
			cur[obj] = knownState{known: true, state: e.applier.Apply(base, ef.Change)}
			if log != nil {
				*log = append(*log, DecisionLogEntry{
					Stage:  "action-apply",
					Input:  fmt.Sprintf("action=%s index=%d object=%s prior=%q change=%q", act.ActionID, idx, obj, base, ef.Change),
					Output: "applied",
					Reason: "object state already known; action is whole-action applicable",
				})
			}
		}
	}

	// 汇总对象级报告。
	for obj := range cover {
		rep := ObjectReport{ObjectID: obj, AnchorIndex: -1}
		switch view.status[obj] {
		case StatusValid:
			rep.Snapshot = StatusValid
		case StatusCorrupt:
			rep.Snapshot = StatusCorrupt
		default:
			rep.Snapshot = StatusAbsent
		}
		if cur[obj].known {
			rep.FinalState = cur[obj].state
			if rep.Snapshot == StatusValid && res.anchors[obj] == -1 {
				rep.Source = SourceSnapshot
			} else {
				rep.Source = SourceReplay
				rep.AnchorIndex = res.anchors[obj]
			}
		} else {
			rep.Source = SourceUnreadable
		}
		res.objects[obj] = rep
	}
	return res
}

// explainAction 产出动作整体放弃判定的可读依据（供判定日志使用）。
func explainAction(act Action, cur map[ObjectID]knownState) (applied bool, blocker ObjectID) {
	ids := make([]ObjectID, 0, len(act.Effects))
	for obj := range act.Effects {
		ids = append(ids, obj)
	}
	sort.Strings(ids)
	for _, obj := range ids {
		ef := act.Effects[obj]
		if !cur[obj].known && !ef.HasStart {
			return false, obj
		}
	}
	return true, ""
}

func formatEffectSet(act Action) string {
	ids := make([]ObjectID, 0, len(act.Effects))
	for obj := range act.Effects {
		ids = append(ids, obj)
	}
	sort.Strings(ids)
	return fmt.Sprint(ids)
}
