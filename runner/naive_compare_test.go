package runner_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

type opSpec struct {
	kind            string
	inst, def, p, a string
	m               uint64
	ceil            uint64
	reqs            []uint64
	now             int64
}

// TestNaiveRandomCompare 用随机掩码与随机操作序列驱动生产 Runner，
// 与逐步朴素模拟逐字段（状态/步骤/缺位/终局/时刻/审计）核对。
func TestNaiveRandomCompare(t *testing.T) {
	const T = int64(7)
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		r, g, c := newKit(t, T)
		model := newNaive(T)

		// 1..3 个定义；ceil 只用位 0..7（位 63 永不进入 ceil）。
		nDef := 1 + rng.Intn(3)
		defNames := make([]string, nDef)
		for d := 0; d < nDef; d++ {
			name := fmt.Sprintf("def%d", d)
			ceil := uint64(1+rng.Intn(255)) & 0xFF
			nSteps := 1 + rng.Intn(4)
			reqs := make([]uint64, nSteps)
			for i := range reqs {
				// 非零且为 ceil 子集：取 ceil 的非空随机子集。
				req := uint64(0)
				for b := 0; b < 8; b++ {
					if ceil&(1<<b) != 0 && rng.Intn(2) == 1 {
						req |= 1 << b
					}
				}
				if req == 0 {
					req = ceil
				}
				reqs[i] = req
			}
			if err := c.Define([]byte(name), ceil, reqs); err != nil {
				t.Fatalf("seed=%d define: %v", seed, err)
			}
			model.defs[name] = append([]uint64(nil), reqs...)
			model.ceils[name] = ceil
			defNames[d] = name
		}

		people := []string{"p", "q", "a", "b"}
		insts := []string{"i", "j", "k"}
		now := int64(0)

		bitSet := []uint64{1, 2, 4, 8, 16, 32, 64, 128, approverBit}
		chooseBit := func() uint64 { return bitSet[rng.Intn(len(bitSet))] }

		for step := 0; step < 120; step++ {
			var op opSpec
			// now 以 0..2 非降推进，制造 deadline 恰等/差一。
			now += int64(rng.Intn(3))
			op.now = now
			switch rng.Intn(9) {
			case 0:
				op.kind = "grant"
				op.p = people[rng.Intn(len(people))]
				op.m = chooseBit()
			case 1:
				op.kind = "revoke"
				op.p = people[rng.Intn(len(people))]
				op.m = chooseBit()
			case 2:
				op.kind = "launch"
				op.inst = insts[rng.Intn(len(insts))]
				op.def = defNames[rng.Intn(nDef)]
				op.p = people[rng.Intn(2)] // 触发者只用 p/q
			case 3:
				op.kind = "start"
				op.inst = insts[rng.Intn(len(insts))]
			case 4:
				op.kind = "finish"
				op.inst = insts[rng.Intn(len(insts))]
			case 5:
				op.kind = "approve"
				op.inst = insts[rng.Intn(len(insts))]
				op.a = people[2+rng.Intn(2)]
			case 6:
				op.kind = "reject"
				op.inst = insts[rng.Intn(len(insts))]
				op.a = people[rng.Intn(len(people))] // 偶尔选到自己 → ErrSelf
			default:
				op.kind = "status"
				op.inst = insts[rng.Intn(len(insts))]
			}

			// 生产端
			var gotErr string
			var gotSt *runner.Status
			switch op.kind {
			case "grant":
				if err := g.Grant([]byte(op.p), op.m); err != nil {
					gotErr = errName(err)
				}
			case "revoke":
				if err := g.Revoke([]byte(op.p), op.m); err != nil {
					gotErr = errName(err)
				}
			case "launch":
				gotErr = errName(r.Launch([]byte(op.inst), []byte(op.def), []byte(op.p), op.now))
			case "start":
				gotErr = errName(r.StartStep([]byte(op.inst), op.now))
			case "finish":
				gotErr = errName(r.FinishStep([]byte(op.inst), op.now))
			case "approve":
				gotErr = errName(r.Approve([]byte(op.inst), []byte(op.a), op.now))
			case "reject":
				gotErr = errName(r.Reject([]byte(op.inst), []byte(op.a), op.now))
			case "status":
				st, err := r.Status([]byte(op.inst), op.now)
				gotErr = errName(err)
				if err == nil {
					gotSt = &st
				}
			}

			// 模型端
			wantErr, wantSt := model.apply(op)
			if gotErr != wantErr {
				t.Fatalf("seed=%d step=%d op=%+v: 错误类 生产=%q 模型=%q",
					seed, step, op, gotErr, wantErr)
			}
			if op.kind == "status" && gotErr == "" {
				assertStatus(t, seed, int64(step), op, gotSt, wantSt)
			}
			if op.kind != "status" && op.kind != "grant" && op.kind != "revoke" {
				assertAudit(t, seed, int64(step), r, op.inst, model.insts[op.inst])
			}
		}
		if testing.Verbose() && seed < 3 {
			t.Logf("seed=%d 完成 120 步随机序列，状态/错误/审计全部与朴素模型一致", seed)
		}
	}
}

func errName(err error) string {
	if err == nil {
		return ""
	}
	for _, pair := range []struct {
		e    error
		name string
	}{
		{runner.ErrArg, "ErrArg"}, {runner.ErrClock, "ErrClock"},
		{runner.ErrNotFound, "ErrNotFound"}, {runner.ErrExists, "ErrExists"},
		{runner.ErrState, "ErrState"}, {runner.ErrSelf, "ErrSelf"},
		{runner.ErrNoAuthority, "ErrNoAuthority"},
		{flow.ErrDef, "ErrDef"}, {grants.ErrArg, "ErrArg"},
	} {
		if errIs(err, pair.e) {
			return pair.name
		}
	}
	return err.Error()
}

func errIs(err, target error) bool {
	type iser interface{ Is(error) bool }
	if x, ok := err.(iser); ok {
		return x.Is(target)
	}
	return err == target
}

func assertStatus(t *testing.T, seed, step int64, op opSpec, got *runner.Status, want *naiveStatus) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("seed=%d step=%d status nil", seed, step)
	}
	wantState := map[naiveState]runner.Phase{
		nActive: runner.PendingPhase, nRunning: runner.RunningPhase,
		nCompleted: runner.CompletedPhase, nFailed: runner.FailedPhase,
	}[want.state]
	if got.Phase != wantState {
		t.Fatalf("seed=%d step=%d op=%+v phase 生产=%v 模型=%v", seed, step, op, got.Phase, want.state)
	}
	if got.Suspended != want.suspend || got.Miss != want.miss || got.Deadline != want.deadline {
		t.Fatalf("seed=%d step=%d 挂起信息 生产={%v %#b %d} 模型={%v %#b %d}",
			seed, step, got.Suspended, got.Miss, got.Deadline, want.suspend, want.miss, want.deadline)
	}
	wantOut := map[string]runner.Outcome{
		"": runner.None, "Completed": runner.Completed,
		"Rejected": runner.Rejected, "Expired": runner.Expired,
	}[want.outcome]
	if got.Outcome != wantOut || got.TerminalAt != want.termAt {
		t.Fatalf("seed=%d step=%d 终局 生产={%v@%d} 模型={%v@%d}",
			seed, step, got.Outcome, got.TerminalAt, want.outcome, want.termAt)
	}
	for i, ws := range want.steps {
		wantStep := map[string]runner.StepPhase{
			"Pending": runner.StepPending, "Running": runner.StepRunning, "Done": runner.StepDone,
		}[ws]
		if got.Steps[i] != wantStep {
			t.Fatalf("seed=%d step=%d 步骤%d 生产=%v 模型=%s", seed, step, i, got.Steps[i], ws)
		}
	}
}

func assertAudit(t *testing.T, seed, step int64, r *runner.Runner, inst string, in *naiveInstance) {
	t.Helper()
	if in == nil {
		return
	}
	evs, err := r.AuditLog([]byte(inst))
	if err != nil {
		t.Fatalf("seed=%d step=%d AuditLog: %v", seed, step, err)
	}
	if len(evs) != len(in.audit) {
		t.Fatalf("seed=%d step=%d inst=%s 审计长度 生产=%d 模型=%d",
			seed, step, inst, len(evs), len(in.audit))
	}
	for i, we := range in.audit {
		ge := evs[i]
		if ge.Seq != i+1 || ge.Kind.String() != we.kind || ge.Step != we.step ||
			ge.At != we.at || ge.Miss != we.miss || string(ge.Approver) != we.approver {
			t.Fatalf("seed=%d step=%d inst=%s 审计[%d] 生产=%+v 模型=%+v",
				seed, step, inst, i, ge, we)
		}
	}
}
