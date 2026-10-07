package replay

// 本文件是校验组件的对外入口，按固定优先级编排三个阶段：
//
//  1. 差异记录自身自洽性校验（check.go）：只读差异记录与快照结构层。
//     若记录自身都不可信，用它驱动任何后续判定都没有意义，故最先判定。
//  2. 前提环境校验 + 受控重放（replay.go）：记录自洽后，才有意义询问
//     “这份记录是否产生自这份快照所代表的环境”。重放在覆盖层上进行，
//     绝不修改原始快照。
//  3. 等价性核对（compare.go）：只有重放完整成功后，才比较重放结果与
//     差异记录声明的目标状态。
//
// 命中靠前类别后立即返回，不再继续后续判定。

// Verifier 是重放校验组件。零值即可使用；不持有任何状态，
// 全部工作数据都在单次调用的栈与覆盖层上，因此可安全并发调用，
// 且对相同输入必然得到相同结果。
type Verifier struct{}

// NewVerifier 构造一个校验组件实例。
func NewVerifier() *Verifier {
	return &Verifier{}
}

// Verify 校验：将差异记录 log 重放到良好快照 snap 之上，
// 判定能否确证与差异记录声明的目标状态等价。
//
// 该方法不修改 snap 与 log，可并发调用且结果确定。
func (v *Verifier) Verify(snap *Snapshot, log *DeltaLog) Result {
	res, _ := v.VerifyWithStats(snap, log)
	return res
}

// VerifyWithStats 与 Verify 相同，但额外返回对原始快照的读取统计，
// 用于复核“校验开销只与差异记录规模相关”这一性质。
func (v *Verifier) VerifyWithStats(snap *Snapshot, log *DeltaLog) (Result, Stats) {
	var stats Stats
	res := v.verify(snap, log, &stats)
	return res, stats
}

func (v *Verifier) verify(snap *Snapshot, log *DeltaLog, stats *Stats) Result {
	if log == nil {
		return Result{Verdict: VerdictInconsistentDelta, ChangeIndex: -1,
			Reason: "差异记录为空"}
	}
	if snap == nil {
		return Result{Verdict: VerdictPreconditionFailed, ChangeIndex: -1,
			Reason: "良好快照缺失（前提环境不存在）"}
	}

	// 阶段 1：差异记录自身自洽性校验。
	if r := checkDelta(snap, log); r != nil {
		return *r
	}

	// 阶段 2 前置：快照索引是前提环境的一部分，缺失即环境不满足。
	if !indexesComplete(snap.Indexes) {
		return Result{Verdict: VerdictPreconditionFailed, ChangeIndex: -1,
			Reason: "良好快照缺少约束/引用完整性索引（前提环境损坏）"}
	}

	// 阶段 2：在覆盖层上受控重放，应用失败即前提环境不满足。
	st := newOverlay(snap, stats)
	if idx, reason := replayLog(st, log); idx >= 0 {
		return Result{Verdict: VerdictPreconditionFailed, ChangeIndex: idx, Reason: reason}
	}

	// 阶段 3：重放结果与声明目标逐项核对。
	mismatches := compareToTarget(st, log)
	if len(mismatches) > 0 {
		return Result{Verdict: VerdictNotEquivalent, ChangeIndex: -1,
			Reason:     "重放结果与差异记录声明的目标状态不等价",
			Mismatches: mismatches,
		}
	}
	return Result{Verdict: VerdictEquivalent, ChangeIndex: -1,
		Reason: "差异记录自洽、前提环境满足、重放结果与声明目标等价"}
}

// indexesComplete 报告快照索引是否完整（五个索引 map 均存在）。
func indexesComplete(idx SnapshotIndexes) bool {
	return idx.ObjectTypeCounts != nil &&
		idx.LinkTypeCounts != nil &&
		idx.LinkEndpointCounts != nil &&
		idx.LinkSourceCounts != nil &&
		idx.PropertyUsageCounts != nil
}
