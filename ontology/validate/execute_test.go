package validate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// 为“组内多钩子全部通过组合 + 至少一个拒绝组合”建立表驱动用例。
// 每个用例两个高优先级组内钩子 h1,h2 + 一个低优先级组钩子 low，
// pattern 给出 h1/h2 的结论（A=approve, R=reject, E=error, P=panic）。
type comboCase struct {
	name         string
	shortCirc    bool
	pattern      [2]byte
	wantCalls    []string // 实际进入的钩子
	wantDecision Decision
	wantGroup    GroupOutcome // 高优先级组结果
	wantError    bool
	wantSkipped  int // 总跳过钩子数
}

func mkHookByByte(id, group string, b byte, calls *[]string) Hook[target] {
	switch b {
	case 'A':
		return approveHook(id, group, calls)
	case 'R':
		return rejectHook(id, group, "bad:"+id, calls)
	case 'E':
		return errorHook(id, group, calls)
	case 'P':
		return panicHook(id, group, calls)
	default:
		panic("bad pattern byte")
	}
}

func TestGroupCombinations(t *testing.T) {
	patterns := []struct {
		pattern [2]byte
		// 允许短路时的预期执行序列与高组结果
		scCalls   []string
		scOutcome GroupOutcome
		scDec     Decision
		scErr     bool
		scSkip    int
		// 不允许短路时
		nsCalls   []string
		nsOutcome GroupOutcome
		nsDec     Decision
		nsErr     bool
		nsSkip    int
	}{
		{[2]byte{'A', 'A'}, []string{"h1", "h2", "low"}, GroupApproved, Approve, false, 0,
			[]string{"h1", "h2", "low"}, GroupApproved, Approve, false, 0},
		{[2]byte{'R', 'A'}, []string{"h1", "low-skip?"}, GroupRejectedShortCircuit, Reject, false, 2,
			[]string{"h1", "h2"}, GroupRejectedAggregated, Reject, false, 1},
		{[2]byte{'A', 'R'}, []string{"h1", "h2"}, GroupRejectedShortCircuit, Reject, false, 1,
			[]string{"h1", "h2"}, GroupRejectedAggregated, Reject, false, 1},
		{[2]byte{'R', 'R'}, []string{"h1"}, GroupRejectedShortCircuit, Reject, false, 2,
			[]string{"h1", "h2"}, GroupRejectedAggregated, Reject, false, 1},
		// 异常：不论分组是否允许短路，异常钩子之后的同组钩子与低优先级组均不执行。
		{[2]byte{'E', 'A'}, []string{"h1"}, GroupIndeterminateError, Approve, true, 2,
			[]string{"h1"}, GroupIndeterminateError, Approve, true, 2},
		{[2]byte{'P', 'A'}, []string{"h1"}, GroupIndeterminateError, Approve, true, 2,
			[]string{"h1"}, GroupIndeterminateError, Approve, true, 2},
		{[2]byte{'A', 'E'}, []string{"h1", "h2"}, GroupIndeterminateError, Approve, true, 1,
			[]string{"h1", "h2"}, GroupIndeterminateError, Approve, true, 1},
	}

	for _, tc := range patterns {
		for _, sc := range []bool{true, false} {
			name := fmt.Sprintf("sc=%v/pat=%c%c", sc, tc.pattern[0], tc.pattern[1])
			t.Run(name, func(t *testing.T) {
				var calls []string
				r := mustRegistry(t,
					GroupSpec{Name: "high", Priority: 100, ShortCircuit: sc},
					GroupSpec{Name: "low", Priority: 1, ShortCircuit: false},
				)
				if err := r.Register(mkHookByByte("h1", "high", tc.pattern[0], &calls)); err != nil {
					t.Fatal(err)
				}
				if err := r.Register(mkHookByByte("h2", "high", tc.pattern[1], &calls)); err != nil {
					t.Fatal(err)
				}
				if err := r.Register(approveHook("low", "low", &calls)); err != nil {
					t.Fatal(err)
				}

				wantCalls := tc.scCalls
				wantOutcome := tc.scOutcome
				wantDec := tc.scDec
				wantErr := tc.scErr
				wantSkip := tc.scSkip
				if !sc {
					wantCalls = tc.nsCalls
					wantOutcome = tc.nsOutcome
					wantDec = tc.nsDec
					wantErr = tc.nsErr
					wantSkip = tc.nsSkip
				}
				// "low-skip?" 只是占位，真实情况下低组钩子绝不进入 calls。
				cleanCalls := make([]string, 0, len(wantCalls))
				for _, c := range wantCalls {
					if c != "low-skip?" {
						cleanCalls = append(cleanCalls, c)
					}
				}

				rep, err := r.Validate(context.Background(), target{value: 1})
				if wantErr {
					if err == nil {
						t.Fatalf("want hook error, got nil (report=%s)", rep.Basis)
					}
					var hee *HookExecutionError
					if !errors.As(err, &hee) {
						t.Fatalf("error type = %T, want *HookExecutionError", err)
					}
					if tc.pattern[0] == 'P' && hee.Panic == nil {
						t.Fatalf("panic must be surfaced with Panic set: %+v", hee)
					}
				} else if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if !reflect.DeepEqual(calls, cleanCalls) {
					t.Fatalf("ran hooks = %v, want %v", calls, cleanCalls)
				}
				if !wantErr && rep.FinalDecision != wantDec {
					t.Fatalf("decision = %v, want %v", rep.FinalDecision, wantDec)
				}
				high := rep.Groups[0]
				if high.Outcome != wantOutcome {
					t.Fatalf("high group outcome = %q, want %q", high.Outcome, wantOutcome)
				}
				lowRes := rep.Groups[1]
				if rep.SkippedHooks() != wantSkip {
					t.Fatalf("skipped = %d, want %d", rep.SkippedHooks(), wantSkip)
				}
				// 审计：被跳过的钩子必须明确标注，且 Outcome 为零值、Err 为 nil，
				// 不得留下“看起来执行过”的痕迹。
				for _, gr := range rep.Groups {
					for _, rec := range gr.Records {
						switch rec.Status {
						case StatusSkippedGroupBlocked, StatusSkippedShortCircuit, StatusSkippedError:
							if rec.Outcome != (Outcome{}) || rec.Err != nil {
								t.Fatalf("skipped record %s/%s must carry no outcome/err: %+v",
									rec.Group, rec.ID, rec)
							}
						}
					}
				}
				// 低优先级组在高组拒绝/异常时必须整体标注 skipped_blocked。
				if high.Outcome != GroupApproved {
					if lowRes.Outcome != GroupSkippedBlocked {
						t.Fatalf("low group outcome = %q, want skipped_blocked", lowRes.Outcome)
					}
					if lowRes.Records[0].Status != StatusSkippedGroupBlocked {
						t.Fatalf("low hook status = %q, want skipped_group_blocked", lowRes.Records[0].Status)
					}
				}
				// 计数一致性：ran + skipped + (无其他状态) == 快照钩子总数。
				total := 0
				for _, g := range rep.Snapshot.Groups {
					total += len(g.Hooks)
				}
				if rep.RanHooks()+rep.SkippedHooks() != total {
					t.Fatalf("ran(%d)+skipped(%d) != snapshot hooks(%d)",
						rep.RanHooks(), rep.SkippedHooks(), total)
				}
			})
		}
	}
}

func TestNonShortCircuitRunsAllBeforeAggregateReject(t *testing.T) {
	var calls []string
	r := mustRegistry(t,
		GroupSpec{Name: "g", Priority: 100, ShortCircuit: false},
		GroupSpec{Name: "low", Priority: 1, ShortCircuit: true},
	)
	for _, id := range []string{"a", "b", "c"} {
		var h Hook[target]
		if id == "b" {
			h = rejectHook(id, "g", "no", &calls)
		} else {
			h = approveHook(id, "g", &calls)
		}
		if err := r.Register(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Register(rejectHook("l", "low", "no", &calls)); err != nil {
		t.Fatal(err)
	}
	rep, err := r.Validate(context.Background(), target{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"a", "b", "c"}) {
		t.Fatalf("non-short-circuit group must run all hooks, calls=%v", calls)
	}
	if rep.FinalDecision != Reject || rep.Groups[0].Outcome != GroupRejectedAggregated {
		t.Fatalf("want aggregated reject, got dec=%v outcomes=%q,%q",
			rep.FinalDecision, rep.Groups[0].Outcome, rep.Groups[1].Outcome)
	}
	if rep.RejectedHooks() != 1 {
		t.Fatalf("rejected count = %d, want 1", rep.RejectedHooks())
	}
	// 审计中可通过 Record 直接核对每个钩子的结论。
	rec, ok := rep.Record("g", "b")
	if !ok || rec.Status != StatusRunRejected {
		t.Fatalf("record b = %+v ok=%v", rec, ok)
	}
	rec, ok = rep.Record("low", "l")
	if !ok || rec.Status != StatusSkippedGroupBlocked {
		t.Fatalf("blocked low hook audit = %+v", rec)
	}
}

func TestErrorDoesNotCountAsRejectAndStopsLowerGroups(t *testing.T) {
	var calls []string
	r := mustRegistry(t,
		GroupSpec{Name: "g1", Priority: 100, ShortCircuit: false}, // 显式不允许短路
		GroupSpec{Name: "g2", Priority: 50, ShortCircuit: true},
		GroupSpec{Name: "g3", Priority: 1, ShortCircuit: true},
	)
	if err := r.Register(approveHook("a", "g1", &calls)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(errorHook("e", "g1", &calls)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(approveHook("after", "g1", &calls)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(approveHook("x2", "g2", &calls)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(approveHook("x3", "g3", &calls)); err != nil {
		t.Fatal(err)
	}
	rep, err := r.Validate(context.Background(), target{})
	if err == nil {
		t.Fatal("error hook must be surfaced as error")
	}
	var hee *HookExecutionError
	if !errors.As(err, &hee) || hee.HookID != "e" || hee.Group != "g1" {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"a", "e"}) {
		t.Fatalf("calls = %v, want [a e]; no hook may run after error", calls)
	}
	if rep.ErroredHooks() != 1 || rep.RejectedHooks() != 0 {
		t.Fatalf("error must not be counted as rejection: err=%d rej=%d",
			rep.ErroredHooks(), rep.RejectedHooks())
	}
	if rep.Groups[0].Outcome != GroupIndeterminateError ||
		rep.Groups[1].Outcome != GroupSkippedBlocked ||
		rep.Groups[2].Outcome != GroupSkippedBlocked {
		t.Fatalf("outcomes = %q,%q,%q", rep.Groups[0].Outcome, rep.Groups[1].Outcome, rep.Groups[2].Outcome)
	}
	if rec, _ := rep.Record("g1", "after"); rec.Status != StatusSkippedError {
		t.Fatalf("same-group hook after error status = %q, want skipped_error", rec.Status)
	}
	// 快照本身保持不变：异常不改变注册表。
	if ids := r.Snapshot().HookIDs(); len(ids) != 5 {
		t.Fatalf("registry mutated by validation: %v", ids)
	}
}

func TestBasisRecordedForEveryOutcome(t *testing.T) {
	var calls []string
	r := mustRegistry(t, GroupSpec{Name: "g", Priority: 1, ShortCircuit: true})
	if err := r.Register(approveHook("a", "g", &calls)); err != nil {
		t.Fatal(err)
	}
	rep, err := r.Validate(context.Background(), target{})
	if err != nil || rep.Basis == "" {
		t.Fatalf("approve path needs non-empty basis: %q err=%v", rep.Basis, err)
	}
	_ = r.Register(rejectHook("b", "g", "no", &calls))
	rep, _ = r.Validate(context.Background(), target{})
	if rep.Basis == "" {
		t.Fatal("reject path needs basis")
	}
}
