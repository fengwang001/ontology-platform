package ontology

import (
	"errors"
	"fmt"
	"testing"
)

var crashSeeds = map[InstanceID]map[string]string{
	"a": {"x": "1"},
	"b": {"y": "2"},
	"c": {"z": "3"},
}

var crashMuts = []Mutation{
	{Instance: "a", Props: map[string]string{"x": "10"}},
	{Instance: "b", Props: map[string]string{"y": "20"}},
	{Instance: "c", Props: map[string]string{"z": "30", "w": "40"}},
}

// runCrashedBatch 构造一次在指定阶段点中断的批次，返回崩溃前的初始状态。
func runCrashedBatch(t *testing.T, disk *SimDisk, audit *AuditLogger, point StagePoint) (pre []Instance, id BatchID) {
	t.Helper()
	e, err := NewEngine(disk, audit, crashHook(point))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	seedInstances(t, e, crashSeeds)
	pre = e.Snapshot()
	id, _, err = e.RunBatch(crashMuts, BatchOptions{})
	if point.Phase == PhaseDone {
		if err != nil {
			t.Fatalf("PhaseDone 不应崩溃: %v", err)
		}
		return pre, id
	}
	if !errors.Is(err, ErrEngineCrashed) {
		t.Fatalf("在 %s 注入中断: err = %v, want ErrEngineCrashed", point, err)
	}
	return pre, id
}

// TestCrashEveryPoint 遍历批次生效过程中的每一个可能中断点：
// 恢复后的最终状态必须准确归入两种终态之一，不存在第三种结果。
func TestCrashEveryPoint(t *testing.T) {
	points := BatchPoints(len(crashMuts))
	commitIdx := commitPointIndex(len(crashMuts))
	for i, point := range points {
		t.Run(fmt.Sprintf("point_%02d_%s", i, point), func(t *testing.T) {
			disk := NewSimDisk()
			audit := NewAuditLogger(nil)
			pre, id := runCrashedBatch(t, disk, audit, point)
			post := expectInstances(pre, id, crashMuts)

			// 重启：恢复前写集内实例必须处于不确定拒绝状态。
			e2, err := NewEngine(disk, audit, nil)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			if point.Phase != PhaseDone && point.Phase != PhaseBeginControl {
				if _, err := e2.Read(crashMuts[0].Instance); !errors.Is(err, ErrInstanceUncertain) {
					t.Fatalf("恢复前读取写集内实例 = %v, want ErrInstanceUncertain", err)
				}
			}

			rep, err := e2.Recover()
			if err != nil {
				t.Fatalf("Recover: %v", err)
			}

			switch {
			case point.Phase == PhaseDone || point.Phase == PhaseBeginControl:
				// Done：批次已干净结束；BeginControl：批次从未出生。
				if rep.HadActiveBatch {
					t.Fatalf("%s 后不应有待恢复批次: %+v", point, rep)
				}
			case i <= commitIdx:
				// COMMIT 落盘之前的中断：必须恢复为“整批未生效”。
				if !rep.HadActiveBatch || rep.Classification != StatusRecoveredUndone {
					t.Fatalf("归类 = %+v, want RECOVERED_UNDONE", rep)
				}
				if rep.CommitRecordFound {
					t.Fatalf("提交点前中断不应发现 COMMIT 记录")
				}
				assertState(t, "恢复为未生效", e2.Snapshot(), pre)
			default:
				// COMMIT 落盘之后的中断：必须恢复为“整批已生效”。
				if !rep.HadActiveBatch || rep.Classification != StatusRecoveredApplied {
					t.Fatalf("归类 = %+v, want RECOVERED_APPLIED", rep)
				}
				if !rep.CommitRecordFound {
					t.Fatalf("提交点后中断必须发现 COMMIT 记录")
				}
				assertState(t, "恢复为已生效", e2.Snapshot(), post)
			}

			// 恢复可重复执行：再次恢复不得改变任何状态。
			before := e2.Snapshot()
			if _, err := e2.Recover(); err != nil {
				t.Fatalf("重复 Recover: %v", err)
			}
			assertState(t, "重复恢复后", e2.Snapshot(), before)

			// 审计：每次中断与恢复判定都必须有完整记录。
			assertAuditConsistent(t, audit)
		})
	}
}

// TestRecoveryInterrupted 遍历恢复管线中的每一个中断点：
// 恢复过程自身被再次中断后，再次恢复必须收敛到同一终态。
func TestRecoveryInterrupted(t *testing.T) {
	points := RecoveryPoints(len(crashMuts))
	// 分别覆盖“恢复为未生效”与“恢复为已生效”两类恢复。
	crashes := []StagePoint{
		{Phase: PhasePreCommit},  // 提交前中断 → 恢复应撤销
		{Phase: PhaseCheckpoint}, // 提交后中断 → 恢复应重做
	}
	for _, bp := range crashes {
		for _, rp := range points {
			t.Run(fmt.Sprintf("batch_%s/recover_%s", bp, rp), func(t *testing.T) {
				disk := NewSimDisk()
				audit := NewAuditLogger(nil)
				pre, id := runCrashedBatch(t, disk, audit, bp)
				post := expectInstances(pre, id, crashMuts)
				wantEffective := bp.Phase == PhaseCheckpoint

				// 第一次恢复在 rp 处再次被中断。
				e2, err := NewEngine(disk, audit, crashHook(rp))
				if err != nil {
					t.Fatalf("NewEngine: %v", err)
				}
				_, err = e2.Recover()
				if rp.Phase == PhaseRDone {
					if err != nil {
						t.Fatalf("RDone 不应崩溃: %v", err)
					}
				} else if !errors.Is(err, ErrEngineCrashed) {
					t.Fatalf("恢复在 %s 中断: err = %v, want ErrEngineCrashed", rp, err)
				}

				// 第二次恢复必须完成并给出相同归类。
				e3, err := NewEngine(disk, audit, nil)
				if err != nil {
					t.Fatalf("NewEngine: %v", err)
				}
				rep, err := e3.Recover()
				if err != nil {
					t.Fatalf("再次 Recover: %v", err)
				}
				if rp.Phase == PhaseRDone {
					if rep.HadActiveBatch {
						t.Fatalf("RDone 后不应有待恢复批次: %+v", rep)
					}
				} else {
					if !rep.HadActiveBatch {
						t.Fatalf("恢复被中断后必须仍有待恢复批次")
					}
					want := StatusRecoveredUndone
					if wantEffective {
						want = StatusRecoveredApplied
					}
					if rep.Classification != want {
						t.Fatalf("归类 = %s, want %s", rep.Classification, want)
					}
				}

				if wantEffective {
					assertState(t, "恢复再中断后终态", e3.Snapshot(), post)
				} else {
					assertState(t, "恢复再中断后终态", e3.Snapshot(), pre)
				}
				assertAuditConsistent(t, audit)
			})
		}
	}
}

// TestRecoveryIdempotent 对同一次中断重复执行恢复多次，
// 终态与对外可见效果必须完全相同，批次不得被重复整体生效。
func TestRecoveryIdempotent(t *testing.T) {
	for _, bp := range []StagePoint{{Phase: PhasePreCommit}, {Phase: PhaseCheckpoint}} {
		t.Run(fmt.Sprintf("batch_%s", bp), func(t *testing.T) {
			disk := NewSimDisk()
			pre, id := runCrashedBatch(t, disk, NewAuditLogger(nil), bp)
			post := expectInstances(pre, id, crashMuts)

			e2 := newEngine(t, disk, nil)
			rep1, err := e2.Recover()
			if err != nil {
				t.Fatalf("Recover#1: %v", err)
			}
			if !rep1.HadActiveBatch {
				t.Fatalf("首次恢复必须处理未决批次: %+v", rep1)
			}
			state1 := e2.Snapshot()
			for k := 0; k < 3; k++ {
				rep, err := e2.Recover()
				if err != nil {
					t.Fatalf("Recover#%d: %v", k+2, err)
				}
				if rep.HadActiveBatch {
					t.Fatalf("恢复完成后不应再有待恢复批次: %+v", rep)
				}
				assertState(t, fmt.Sprintf("第%d次重复恢复", k+2), e2.Snapshot(), state1)
			}
			// 版本号不得因重复恢复而多次递增。
			if bp.Phase == PhaseCheckpoint {
				assertState(t, "终态", e2.Snapshot(), post)
			} else {
				assertState(t, "终态", e2.Snapshot(), pre)
			}
		})
	}
}

// TestTornCommitRecord 模拟 COMMIT 记录被撕裂写入（崩溃发生在写提交记录
// 的中途）：损坏的提交记录不得被认定为“已生效”，必须归为未生效。
func TestTornCommitRecord(t *testing.T) {
	disk := NewSimDisk()
	pre, id := runCrashedBatch(t, disk, NewAuditLogger(nil), StagePoint{Phase: PhaseCheckpoint})

	// 撕裂 COMMIT 记录：截断段文件到 COMMIT 行的中间。
	data, err := disk.ReadFile(segmentFile(id))
	if err != nil {
		t.Fatalf("读取段文件: %v", err)
	}
	jd := scanJournal(disk, id)
	if !jd.HasCommit {
		t.Fatalf("前置条件失败：应存在 COMMIT 记录")
	}
	disk.WriteFile(segmentFile(id), data[:len(data)-5]) // 截掉 COMMIT 行尾部
	disk.Sync(segmentFile(id))

	e2 := newEngine(t, disk, nil)
	rep, err := e2.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.CommitRecordFound || rep.Classification != StatusRecoveredUndone {
		t.Fatalf("撕裂的 COMMIT 必须归为未生效: %+v", rep)
	}
	assertState(t, "撕裂提交恢复", e2.Snapshot(), pre)
}

// TestRecoveredIndistinguishable 验证四类状态的可观察效果：
// 恢复为未生效 ≡ 正常回退；恢复为已生效 ≡ 正常生效。
func TestRecoveredIndistinguishable(t *testing.T) {
	build := func(t *testing.T, run func(e *Engine) (BatchID, BatchStatus)) []Instance {
		disk := NewSimDisk()
		e := newEngine(t, disk, nil)
		seedInstances(t, e, crashSeeds)
		id, st := run(e)
		if got, _ := e.BatchStatus(id); got != st {
			t.Fatalf("BatchStatus = %s, want %s", got, st)
		}
		return e.Snapshot()
	}

	committed := build(t, func(e *Engine) (BatchID, BatchStatus) {
		id, st, err := e.RunBatch(crashMuts, BatchOptions{})
		if err != nil {
			t.Fatalf("RunBatch: %v", err)
		}
		return id, st
	})
	rolledBack := build(t, func(e *Engine) (BatchID, BatchStatus) {
		id, st, err := e.RunBatch(crashMuts, BatchOptions{AbortAtPreCommit: true})
		if err != nil {
			t.Fatalf("RunBatch: %v", err)
		}
		return id, st
	})
	recovered := func(point StagePoint) ([]Instance, BatchStatus) {
		disk := NewSimDisk()
		runCrashedBatch(t, disk, NewAuditLogger(nil), point)
		e := newEngine(t, disk, nil)
		rep, err := e.Recover()
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		return e.Snapshot(), rep.Classification
	}
	undone, stU := recovered(StagePoint{Phase: PhasePreCommit})
	applied, stA := recovered(StagePoint{Phase: PhaseCheckpoint})

	if stU != StatusRecoveredUndone || stA != StatusRecoveredApplied {
		t.Fatalf("归类错误: %s / %s", stU, stA)
	}
	assertState(t, "恢复未生效 ≡ 正常回退", undone, rolledBack)
	assertState(t, "恢复已生效 ≡ 正常生效", applied, committed)
}

// TestRecoveryScanBounded 验证恢复扫描的历史记录量只与本批次规模相关，
// 不随系统处理过的批次总数增长。证据来自恢复报告本身与磁盘读记录，
// 不依赖任何额外对外暴露的状态。
func TestRecoveryScanBounded(t *testing.T) {
	disk := NewSimDisk()
	armed := false
	hook := func(p StagePoint) bool {
		return armed && p == (StagePoint{Phase: PhaseCheckpoint})
	}
	e := newEngine(t, disk, hook)
	seedInstances(t, e, crashSeeds)

	// 先干净地处理一大批历史批次。
	const history = 50
	for k := 0; k < history; k++ {
		if _, st, err := e.RunBatch(crashMuts, BatchOptions{}); err != nil || st != StatusCommitted {
			t.Fatalf("历史批次 %d: %v %s", k, err, st)
		}
	}
	// 干净结束后磁盘上不应残留任何历史日志段。
	if segs := disk.List("journal/"); len(segs) != 0 {
		t.Fatalf("历史日志段未清理: %v", segs)
	}

	// 第 51 个批次在提交后崩溃。
	armed = true
	if _, _, err := e.RunBatch(crashMuts, BatchOptions{}); !errors.Is(err, ErrEngineCrashed) {
		t.Fatalf("err = %v, want ErrEngineCrashed", err)
	}

	rec := newRecordingDisk(disk)
	e2, err := NewEngine(rec, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	rec.reset() // 只统计 Recover 期间的读取
	rep, err := e2.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// 遍历的记录量 = 本批次自身的记录数（BEGIN + 3*MUT + COMMIT），
	// 与之前处理的 50 个历史批次无关。
	wantRecords := 1 + len(crashMuts) + 1
	if rep.JournalRecords != wantRecords {
		t.Fatalf("扫描记录数 = %d, want %d（不随历史批次总数增长）", rep.JournalRecords, wantRecords)
	}
	// 恢复期间只读取了本批次的段文件，未触碰任何其他历史记录。
	for _, f := range rec.reads {
		if f != segmentFile(rep.Batch) {
			t.Fatalf("恢复期间读取了批次历史之外的文件: %s", f)
		}
	}
}

// assertAuditConsistent 校验审计日志的完整性与确定性：
//   - 每次中断都记录了发生的阶段；
//   - 每次恢复判定的归类结果只能是两种恢复终态之一；
//   - 同一批次的多次恢复判定必须一致（判定确定且可复现）；
//   - 有实际中断发生时，必须存在对应的恢复判定记录。
func assertAuditConsistent(t *testing.T, audit *AuditLogger) {
	t.Helper()
	var crashesWithBatch, decisions int
	byBatch := make(map[BatchID]BatchStatus)
	for _, ev := range audit.Events() {
		switch ev.Kind {
		case AuditCrash:
			if ev.Stage == "" {
				t.Fatalf("中断事件缺少阶段记录: %+v", ev)
			}
			if ev.Batch != 0 {
				crashesWithBatch++
			}
		case AuditRecoverDec, AuditRecover:
			decisions++
			if ev.Classification != StatusRecoveredUndone && ev.Classification != StatusRecoveredApplied {
				t.Fatalf("恢复归类必须是两种终态之一: %+v", ev)
			}
			if prev, ok := byBatch[ev.Batch]; ok && prev != ev.Classification {
				t.Fatalf("批次 %d 的恢复判定不一致: %s vs %s", ev.Batch, prev, ev.Classification)
			}
			byBatch[ev.Batch] = ev.Classification
		}
	}
	if crashesWithBatch > 0 && decisions == 0 {
		t.Fatalf("有中断发生但缺少恢复判定记录: crashes=%d", crashesWithBatch)
	}
}
