package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// TestRandomizedAgainstNaiveModel 针对随机生成的批量更新与中断注入序列，
// 将恢复后的最终状态与独立实现的朴素全量重放模型对照，要求完全一致。
// 每次中断的阶段、恢复判定依据与归类都记录在审计日志中以便重放核验。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	seeds := []int64{1, 7, 42, 1696, 20261007, 99, 31337, 555, 808, 123456789}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			runRandomScenario(t, seed)
		})
	}
}

func runRandomScenario(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	disk := NewSimDisk()
	audit := NewAuditLogger(nil)
	naive := NewNaiveModel()

	// pendingCrash 非空时，下一次引擎调用在该阶段点崩溃。
	var pendingCrash *StagePoint
	hook := func(p StagePoint) bool {
		if pendingCrash != nil && p == *pendingCrash {
			pendingCrash = nil
			return true
		}
		return false
	}
	e, err := NewEngine(disk, audit, hook)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// 初始实例。
	const nInstances = 6
	ids := make([]InstanceID, nInstances)
	for i := 0; i < nInstances; i++ {
		ids[i] = InstanceID(fmt.Sprintf("inst-%d", i))
		props := map[string]string{"seed": fmt.Sprintf("s%d", rng.Intn(100))}
		if err := e.Write(ids[i], props); err != nil {
			t.Fatalf("seed write: %v", err)
		}
		naive.AddWrite(ids[i], props)
	}

	// restart 模拟一次进程重启（崩溃后磁盘已丢弃未落盘数据）。
	restart := func() {
		e, err = NewEngine(disk, audit, hook)
		if err != nil {
			t.Fatalf("restart NewEngine: %v", err)
		}
	}
	// recoverFully 完成恢复（不再注入中断），返回恢复报告。
	recoverFully := func(round int) RecoverReport {
		for {
			rep, err := e.Recover()
			if errors.Is(err, ErrEngineCrashed) {
				restart()
				continue
			}
			if err != nil {
				t.Fatalf("round=%d Recover: %v", round, err)
			}
			if rep.HadActiveBatch {
				t.Logf("round=%d 恢复判定: batch=%d commitFound=%v 归类=%s 扫描记录=%d",
					round, rep.Batch, rep.CommitRecordFound, rep.Classification, rep.JournalRecords)
			}
			return rep
		}
	}

	const rounds = 40
	for round := 0; round < rounds; round++ {
		if rng.Intn(100) < 30 {
			// 单实例写入。
			id := ids[rng.Intn(nInstances)]
			props := map[string]string{fmt.Sprintf("k%d", rng.Intn(4)): fmt.Sprintf("v%d", rng.Intn(1000))}
			if err := e.Write(id, props); err != nil {
				t.Fatalf("round=%d Write: %v", round, err)
			}
			naive.AddWrite(id, props)
			continue
		}

		// 批量更新：随机 1~3 个不同实例。
		nMut := 1 + rng.Intn(3)
		perm := rng.Perm(nInstances)[:nMut]
		muts := make([]Mutation, 0, nMut)
		for _, idx := range perm {
			muts = append(muts, Mutation{
				Instance: ids[idx],
				Props:    map[string]string{fmt.Sprintf("k%d", rng.Intn(4)): fmt.Sprintf("b%d", rng.Intn(1000))},
			})
		}

		fate := rng.Intn(100)
		switch {
		case fate < 35:
			// 正常生效。
			id, st, err := e.RunBatch(muts, BatchOptions{})
			if err != nil || st != StatusCommitted {
				t.Fatalf("round=%d 正常批次: %v %s", round, err, st)
			}
			naive.AddBatch(id, muts, true)
		case fate < 45:
			// 正常回退。
			id, st, err := e.RunBatch(muts, BatchOptions{AbortAtPreCommit: true})
			if err != nil || st != StatusRolledBack {
				t.Fatalf("round=%d 回退批次: %v %s", round, err, st)
			}
			naive.AddBatch(id, muts, false)
		default:
			// 在随机阶段点注入中断。
			points := BatchPoints(nMut)
			crashIdx := rng.Intn(len(points))
			pendingCrash = &points[crashIdx]
			id, _, err := e.RunBatch(muts, BatchOptions{})
			if err == nil {
				// 注入点是 PhaseDone：批次干净完成。
				naive.AddBatch(id, muts, true)
				continue
			}
			if !errors.Is(err, ErrEngineCrashed) {
				t.Fatalf("round=%d 批次中断: %v", round, err)
			}
			t.Logf("round=%d 批次 %d 在阶段 %s 被中断", round, id, points[crashIdx])
			restart()
			// 恢复过程自身可能再次被随机中断（0~2 次）。
			recovered := false
			for k := 0; k < rng.Intn(3) && !recovered; k++ {
				rpts := RecoveryPoints(nMut)
				rp := rpts[rng.Intn(len(rpts))]
				pendingCrash = &rp
				rep, err := e.Recover()
				pendingCrash = nil
				if errors.Is(err, ErrEngineCrashed) {
					t.Logf("round=%d 恢复在阶段 %s 再次被中断", round, rp)
					restart()
					continue
				}
				if err != nil {
					t.Fatalf("round=%d Recover: %v", round, err)
				}
				recordNaiveFromReport(t, naive, rep, muts)
				recovered = true
			}
			if !recovered {
				rep := recoverFully(round)
				recordNaiveFromReport(t, naive, rep, muts)
			}
		}
	}

	// 终态对照：引擎状态必须与朴素全量重放模型完全一致。
	assertState(t, "随机场景终态对照朴素模型", e.Snapshot(), naive.State())

	// 审计完整性：每次中断都有记录，同一批次判定一致。
	assertAuditConsistent(t, audit)
}

// recordNaiveFromReport 按恢复报告的归类把批次记入朴素模型。
func recordNaiveFromReport(t *testing.T, naive *NaiveModel, rep RecoverReport, muts []Mutation) {
	t.Helper()
	if !rep.HadActiveBatch {
		return // 批次从未出生（控制记录未落盘），等价于从未发生
	}
	naive.AddBatch(rep.Batch, muts, rep.Classification.Effective())
}
