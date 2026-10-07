package ontology

// RecoverReport 记录一次恢复执行的完整判定依据与结果，供审计与核验。
type RecoverReport struct {
	HadActiveBatch bool    `json:"had_active_batch"`
	Batch          BatchID `json:"batch,omitempty"`
	// CommitRecordFound 是唯一判定依据：活跃批次段中是否存在完整 COMMIT 记录。
	CommitRecordFound bool `json:"commit_record_found"`
	// Classification 是恢复归类的终态（仅当 HadActiveBatch 为真）。
	Classification BatchStatus `json:"classification,omitempty"`
	// JournalBytes/JournalRecords 是本次恢复实际遍历的历史记录量，
	// 只与本批次规模相关，与系统历史批次总数无关。
	JournalBytes   int `json:"journal_bytes"`
	JournalRecords int `json:"journal_records"`
	// Fixes 是执行的幂等撤销/重做次数。
	Fixes int `json:"fixes"`
	// Repeated 为真表示这是对同一中断的重复恢复（此前已有恢复完成）。
	Repeated bool `json:"repeated"`
}

// Recover 对未决批次执行恢复，可安全重复调用。
//
// 判定规则（确定且可复现）：
//   - 批次段中存在完整 COMMIT 记录 → 批次已被外部认定为已生效，
//     对所有变更执行幂等重做，终态为 StatusRecoveredApplied；
//   - 否则 → 批次未被认定为已生效，对所有已应用变更执行幂等撤销，
//     终态为 StatusRecoveredUndone。
//
// 重做与撤销都以实例的 LastBatch 戳为条件，因此无论恢复被中断多少次、
// 重复执行多少次，都收敛到同一个终态，且批次不会被重复整体生效。
func (e *Engine) Recover() (report RecoverReport, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(crashSignal); ok {
				err = ErrEngineCrashed
				return
			}
			panic(r)
		}
	}()
	if err := e.guard(); err != nil {
		return report, err
	}

	id := e.ctrl.ActiveBatch
	if id == 0 {
		return report, nil // 无未决批次：恢复是空操作
	}
	report.HadActiveBatch = true
	report.Batch = id
	// 若状态文件已存在，说明此前某次恢复已收尾完成，本次为重复恢复。
	if e.disk.Exists(statusFile(id)) {
		report.Repeated = true
	}

	// 阶段 RScan：只读取 CONTROL 与这一个批次的段文件。
	e.point(StagePoint{Phase: PhaseRScan})
	jd := scanJournal(e.disk, id)
	if jd.SegmentErr != nil {
		return report, jd.SegmentErr
	}
	report.JournalBytes = jd.BytesRead
	report.JournalRecords = len(jd.Records)
	report.CommitRecordFound = jd.HasCommit
	if jd.HasCommit {
		report.Classification = StatusRecoveredApplied
	} else {
		report.Classification = StatusRecoveredUndone
	}
	// 判定在扫描完成后立即确定并记录：即使恢复随后再次被中断，
	// 判定依据也已留痕，且重复恢复必须给出相同归类（确定性）。
	if e.audit != nil {
		e.audit.Log(AuditEvent{
			Kind:              AuditRecoverDec,
			Batch:             id,
			CommitRecordFound: report.CommitRecordFound,
			Classification:    report.Classification,
			JournalBytes:      report.JournalBytes,
			JournalRecords:    report.JournalRecords,
		})
	}

	// 阶段 RFix[i]：按判定结果对每条变更执行幂等撤销或重做。
	for i, rec := range jd.Muts {
		e.point(StagePoint{Phase: PhaseRFix, Seq: i})
		cur, ok := e.store.get(rec.Instance)
		if jd.HasCommit {
			// 重做：仅当该实例尚未带上本批次戳时应用后像。
			if !ok || cur.LastBatch != id {
				e.store.apply(Instance{
					ID:        rec.Instance,
					Version:   rec.After.Version,
					LastBatch: rec.After.LastBatch,
					Props:     rec.After.Props,
				})
				e.store.syncDelta()
				report.Fixes++
			}
		} else {
			// 撤销：仅当该实例仍带着本批次戳时恢复前像。
			if ok && cur.LastBatch == id {
				e.store.apply(Instance{
					ID:        rec.Instance,
					Version:   rec.Before.Version,
					LastBatch: rec.Before.LastBatch,
					Props:     rec.Before.Props,
				})
				e.store.syncDelta()
				report.Fixes++
			}
		}
	}

	// 阶段 RFinalize：检查点、状态落盘、CONTROL 复位、段删除、解锁。
	e.point(StagePoint{Phase: PhaseRFinalize})
	// 写集锁以 CONTROL 中的 ActiveWriteSet 为准：
	// BEGIN 记录可能因崩溃丢失（批次尚未在段中留下任何记录），
	// 但 CONTROL 中的写集一定已随批次出生证明落盘。
	writeSet := e.ctrl.ActiveWriteSet
	e.store.checkpoint()
	e.ctrl.ActiveBatch = 0
	e.ctrl.ActiveWriteSet = nil
	e.writeControl()
	e.writeStatus(id, report.Classification)
	e.disk.Delete(segmentFile(id))
	for _, inst := range writeSet {
		delete(e.locked, inst)
	}
	if jd.Begin != nil {
		for _, m := range jd.Begin.Muts {
			delete(e.locked, m.Instance)
		}
	}
	delete(e.inFlight, id)
	if e.audit != nil {
		e.audit.Log(AuditEvent{
			Kind:              AuditRecover,
			Batch:             id,
			CommitRecordFound: report.CommitRecordFound,
			Classification:    report.Classification,
			JournalBytes:      report.JournalBytes,
			JournalRecords:    report.JournalRecords,
			Fixes:             report.Fixes,
			Repeated:          report.Repeated,
		})
	}
	return report, nil
}
