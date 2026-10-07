package audit

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestRandomDifferential 用大量随机“提交/回退/审计失败/订正”序列，
// 把快速重放器与朴素线性模型在大量随机区间上逐条对照（多组种子）。
func TestRandomDifferential(t *testing.T) {
	tl := &testLogger{}
	defer tl.flush(t)

	for _, seed := range []int64{1, 2, 3, 42, timeNowNano()} {
		tl.logf("=== 随机种子: %d ===", seed)
		if !runOneDifferential(t, tl, seed) {
			return
		}
	}
}

func runOneDifferential(t *testing.T, tl *testLogger, seed int64) bool {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	store, log, exec, replayer, naive := newSystem(7)
	const typ = "Obj"
	const instances = 6
	for i := 0; i < instances; i++ {
		mustRegister(t, exec, typ, fmt.Sprintf("o%d", i), "init")
	}

	valSeq := 0
	nextVal := func() string { valSeq++; return fmt.Sprintf("v%d", valSeq) }

	correctable := []int64{}
	const rounds = 300

	for round := 0; round < rounds; round++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4: // 提交
			ws, act := randomAction(rng, typ, round, instances, nextVal)
			rec, err := exec.Execute(act, nil)
			if err != nil {
				t.Fatalf("round %d execute: %v (writes=%v)", round, err, ws)
			}
			correctable = append(correctable, rec.Seq)
			tl.logf("[%d] 提交 %s", round, rec)
		case 5, 6: // 业务回退
			_, act := randomAction(rng, typ, round, instances, nextVal)
			rec, err := exec.Execute(act, func(*Txn) error { return ErrActionRolledBack })
			if err != nil {
				t.Fatalf("round %d rollback: %v", round, err)
			}
			tl.logf("[%d] 回退 %s", round, rec)
		case 7: // 审计写入失败
			_, act := randomAction(rng, typ, round, instances, nextVal)
			log.InjectNextAppendFailure(fmt.Errorf("injected IO failure"))
			_, err := exec.Execute(act, nil)
			if !isAuditWrite(err) {
				t.Fatalf("round %d want AuditWriteError, got %v", round, err)
			}
			tl.logf("[%d] 审计失败（已整体回退）: %v", round, err)
		case 8: // 参数非法：目标实例不存在
			bad := Action{
				ActionID: fmt.Sprintf("act-%d-illegal", round),
				TypeName: typ,
				Writes:   []Write{{"ghost", nextVal()}},
			}
			_, err := exec.Execute(bad, nil)
			if !isIllegal(err) {
				t.Fatalf("round %d want IllegalRequestError, got %v", round, err)
			}
			tl.logf("[%d] 参数非法: %v", round, err)
		case 9: // 订正某条已提交动作
			if len(correctable) == 0 {
				break
			}
			idx := rng.Intn(len(correctable))
			target := correctable[idx]
			rec, ok := log.At(typ, target)
			if !ok {
				t.Fatalf("missing target %d", target)
			}
			fixed := make(map[string]string, len(rec.Changes))
			for _, c := range rec.Changes {
				fixed[c.Instance] = nextVal()
			}
			corr, err := exec.Correct(typ, fmt.Sprintf("corr-%d->%d", round, target), target, fixed)
			if err != nil {
				t.Fatalf("round %d correct: %v", round, err)
			}
			// 订正后该动作已被订正，不允许再次作为订正目标来源
			// 新增动作仍可订正；这里从可订正集合移除目标。
			correctable = append(correctable[:idx], correctable[idx+1:]...)
			tl.logf("[%d] 订正 %s", round, corr)
		}

		// 每轮在若干随机区间对照快速重放与朴素模型。
		last := log.LastSeq(typ)
		for s := 0; s < 3 && last > 0; s++ {
			to := int64(1 + rng.Intn(int(last)))
			from := int64(rng.Intn(int(to) + 1))
			got, err := replayer.Replay(typ, from, to)
			if err != nil {
				t.Errorf("seed %d round %d replay: %v", seed, round, err)
				return false
			}
			want, err := naive.Replay(typ, from, to)
			if err != nil {
				t.Errorf("seed %d round %d naive: %v", seed, round, err)
				return false
			}
			if !replayEqual(got, want) {
				tl.logf("区间 (%d,%d] 不一致!\n快速=%+v\n朴素=%+v", from, to, got, want)
				t.Errorf("seed %d differential mismatch at round %d range (%d,%d]", seed, round, from, to)
				return false
			}
		}

		// 全量终态：重放结果必须等于活动状态。
		final, err := replayer.StateAt(typ, log.LastSeq(typ))
		if err != nil {
			t.Errorf("replay: %v", err)
			return false
		}
		if !stateEqual(final, store.Snapshot(typ)) {
			t.Errorf("seed %d round %d live/replay drift: %v vs %v", seed, round, final, store.Snapshot(typ))
			return false
		}
	}

	last := log.LastSeq(typ)
	got, _ := replayer.Replay(typ, 0, last)
	want, _ := naive.Replay(typ, 0, last)
	tl.logf("种子 %d 最终 seq=%d 快速=%v | 朴素=%v | 活动=%v",
		seed,
		last, got.StateTo, want.StateTo, store.Snapshot(typ))
	if !replayEqual(got, want) {
		t.Errorf("seed %d final differential mismatch", seed)
		return false
	}
	if err := log.VerifyChain(typ); err != nil {
		t.Errorf("chain: %v", err)
		return false
	}
	tl.logf("种子 %d 依据: %d 轮随机输入，每轮 3 个随机区间 + 全量终态逐条一致", seed, rounds)
	return true
}

func randomAction(rng *rand.Rand, typ string, round, instances int, nextVal func() string) ([]Write, Action) {
	k := 1 + rng.Intn(instances)
	used := map[string]bool{}
	writes := make([]Write, 0, k)
	for len(writes) < k {
		idx := rng.Intn(instances)
		inst := fmt.Sprintf("o%d", idx)
		if used[inst] {
			continue
		}
		used[inst] = true
		writes = append(writes, Write{inst, nextVal()})
	}
	return writes, Action{
		ActionID: fmt.Sprintf("act-%d", round),
		TypeName: typ,
		Writes:   writes,
	}
}

func replayEqual(a, b ReplayResult) bool {
	if a.FromSeq != b.FromSeq || a.ToSeq != b.ToSeq {
		return false
	}
	if !stateEqual(a.StateFrom, b.StateFrom) || !stateEqual(a.StateTo, b.StateTo) {
		return false
	}
	if len(a.Events) != len(b.Events) {
		return false
	}
	for i := range a.Events {
		ea, eb := a.Events[i], b.Events[i]
		if ea.Seq != eb.Seq || ea.Kind != eb.Kind || ea.ActionID != eb.ActionID {
			return false
		}
		if len(ea.Changes) != len(eb.Changes) {
			return false
		}
		for j := range ea.Changes {
			if ea.Changes[j] != eb.Changes[j] {
				return false
			}
		}
	}
	return true
}
