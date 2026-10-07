package ontology

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

func newValidateRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	mustDeclare(t, r,
		GroupSpec{Name: "sc-high", Priority: 0, ShortCircuit: true},
		GroupSpec{Name: "agg-mid", Priority: 1, ShortCircuit: false},
		GroupSpec{Name: "sc-low", Priority: 2, ShortCircuit: true},
	)
	return r
}

func groupRecord(t *testing.T, rep Report, name string) GroupRecord {
	t.Helper()
	for _, g := range rep.Groups {
		if g.Group.Name == name {
			return g
		}
	}
	t.Fatalf("group %q not found in report", name)
	return GroupRecord{}
}

func hookStatuses(g GroupRecord) map[string]HookStatus {
	out := make(map[string]HookStatus, len(g.Hooks))
	for _, h := range g.Hooks {
		out[h.ID] = h.Status
	}
	return out
}

func TestShortCircuitGroupAllPass(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "sc", Priority: 0, ShortCircuit: true})
	rec := &execRecorder{}
	mustRegister(t, r, "sc", "h1", rec.hook("h1", Allow, nil))
	mustRegister(t, r, "sc", "h2", rec.hook("h2", Allow, nil))
	mustRegister(t, r, "sc", "h3", rec.hook("h3", Allow, nil))

	rep, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "Employee"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rep.Decision != Allow {
		t.Fatalf("Decision = %v, want allow", rep.Decision)
	}
	if got := fmt.Sprint(rec.called()); got != "[h1 h2 h3]" {
		t.Fatalf("executed = %v, want [h1 h2 h3]", got)
	}
	g := groupRecord(t, rep, "sc")
	if g.Outcome != OutcomePassed {
		t.Fatalf("Outcome = %v, want passed", g.Outcome)
	}
	if rep.SkippedCount() != 0 {
		t.Fatalf("SkippedCount = %d, want 0", rep.SkippedCount())
	}
}

func TestShortCircuitGroupRejectStopsGroupAndLowerGroups(t *testing.T) {
	r := newValidateRegistry(t)
	rec := &execRecorder{}
	mustRegister(t, r, "sc-high", "h1", rec.hook("h1", Allow, nil))
	mustRegister(t, r, "sc-high", "h2", rec.hook("h2", Reject, nil))
	mustRegister(t, r, "sc-high", "h3", rec.hook("h3", Allow, nil))
	mustRegister(t, r, "agg-mid", "m1", rec.hook("m1", Allow, nil))
	mustRegister(t, r, "sc-low", "l1", rec.hook("l1", Allow, nil))

	rep, err := r.Validate(context.Background(), Target{Kind: "linkType", Name: "WorksFor"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rep.Decision != Reject {
		t.Fatalf("Decision = %v, want reject", rep.Decision)
	}

	// h3 被组内短路；低优先级分组真正未执行，无任何执行痕迹。
	if got := fmt.Sprint(rec.called()); got != "[h1 h2]" {
		t.Fatalf("executed = %v, want [h1 h2]", got)
	}

	high := groupRecord(t, rep, "sc-high")
	if high.Outcome != OutcomeRejectedShortCircuit {
		t.Fatalf("high Outcome = %v, want %v", high.Outcome, OutcomeRejectedShortCircuit)
	}
	st := hookStatuses(high)
	if st["h1"] != HookPassed || st["h2"] != HookRejected || st["h3"] != HookSkipped {
		t.Fatalf("high statuses = %v", st)
	}

	// 低优先级分组：允许保留明确标注为未执行的审计记录。
	for _, name := range []string{"agg-mid", "sc-low"} {
		g := groupRecord(t, rep, name)
		if g.Outcome != OutcomeNotExecuted {
			t.Fatalf("%s Outcome = %v, want %v", name, g.Outcome, OutcomeNotExecuted)
		}
		for _, h := range g.Hooks {
			if h.Status != HookNotExecuted {
				t.Fatalf("%s/%s status = %v, want %v", name, h.ID, h.Status, HookNotExecuted)
			}
		}
	}

	// 跳过数 = 组内短路 1 + 整组未执行 2。
	if got := rep.SkippedCount(); got != 3 {
		t.Fatalf("SkippedCount = %d, want 3", got)
	}
}

func TestNonShortCircuitGroupAllPass(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "agg", Priority: 0, ShortCircuit: false})
	rec := &execRecorder{}
	mustRegister(t, r, "agg", "h1", rec.hook("h1", Allow, nil))
	mustRegister(t, r, "agg", "h2", rec.hook("h2", Allow, nil))

	rep, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "Company"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rep.Decision != Allow {
		t.Fatalf("Decision = %v, want allow", rep.Decision)
	}
	if got := fmt.Sprint(rec.called()); got != "[h1 h2]" {
		t.Fatalf("executed = %v, want [h1 h2]", got)
	}
	if g := groupRecord(t, rep, "agg"); g.Outcome != OutcomePassed {
		t.Fatalf("Outcome = %v, want passed", g.Outcome)
	}
}

func TestNonShortCircuitGroupRejectAggregates(t *testing.T) {
	r := newValidateRegistry(t)
	rec := &execRecorder{}
	mustRegister(t, r, "sc-high", "h1", rec.hook("h1", Allow, nil))
	// 不允许短路：拒绝后仍须执行完组内全部钩子再汇总。
	mustRegister(t, r, "agg-mid", "m1", rec.hook("m1", Reject, nil))
	mustRegister(t, r, "agg-mid", "m2", rec.hook("m2", Allow, nil))
	mustRegister(t, r, "agg-mid", "m3", rec.hook("m3", Reject, nil))
	mustRegister(t, r, "sc-low", "l1", rec.hook("l1", Allow, nil))

	rep, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "Vendor"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rep.Decision != Reject {
		t.Fatalf("Decision = %v, want reject", rep.Decision)
	}
	if got := fmt.Sprint(rec.called()); got != "[h1 m1 m2 m3]" {
		t.Fatalf("executed = %v, want [h1 m1 m2 m3]", got)
	}

	mid := groupRecord(t, rep, "agg-mid")
	if mid.Outcome != OutcomeRejectedAggregate {
		t.Fatalf("mid Outcome = %v, want %v", mid.Outcome, OutcomeRejectedAggregate)
	}
	st := hookStatuses(mid)
	if st["m1"] != HookRejected || st["m2"] != HookPassed || st["m3"] != HookRejected {
		t.Fatalf("mid statuses = %v", st)
	}

	low := groupRecord(t, rep, "sc-low")
	if low.Outcome != OutcomeNotExecuted {
		t.Fatalf("low Outcome = %v, want %v", low.Outcome, OutcomeNotExecuted)
	}
	if got := rep.SkippedCount(); got != 1 {
		t.Fatalf("SkippedCount = %d, want 1", got)
	}
}

func TestHookErrorIsIndeterminateAndPropagated(t *testing.T) {
	for _, shortCircuit := range []bool{true, false} {
		t.Run(fmt.Sprintf("shortCircuit=%v", shortCircuit), func(t *testing.T) {
			r := NewRegistry()
			mustDeclare(t, r,
				GroupSpec{Name: "g0", Priority: 0, ShortCircuit: shortCircuit},
				GroupSpec{Name: "g1", Priority: 1, ShortCircuit: true},
			)
			rec := &execRecorder{}
			mustRegister(t, r, "g0", "ok", rec.hook("ok", Allow, nil))
			mustRegister(t, r, "g0", "bad", rec.hook("bad", Allow, errBoom))
			mustRegister(t, r, "g0", "after", rec.hook("after", Allow, nil))
			mustRegister(t, r, "g1", "lower", rec.hook("lower", Allow, nil))

			rep, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "X"})

			// 异常必须单独上抛，不得归并为普通拒绝。
			var hookErr *HookError
			if !errors.As(err, &hookErr) {
				t.Fatalf("err = %v, want *HookError", err)
			}
			if !errors.Is(err, errBoom) {
				t.Fatalf("err chain missing errBoom: %v", err)
			}
			if hookErr.Group != "g0" || hookErr.HookID != "bad" {
				t.Fatalf("HookError = %+v", hookErr)
			}

			// 不论该分组是否允许短路，更低优先级分组一律不得执行。
			if got := fmt.Sprint(rec.called()); got != "[ok bad]" {
				t.Fatalf("executed = %v, want [ok bad]", got)
			}
			g0 := groupRecord(t, rep, "g0")
			if g0.Outcome != OutcomeIndeterminate {
				t.Fatalf("g0 Outcome = %v, want %v", g0.Outcome, OutcomeIndeterminate)
			}
			st := hookStatuses(g0)
			if st["ok"] != HookPassed || st["bad"] != HookErrored || st["after"] != HookSkipped {
				t.Fatalf("g0 statuses = %v", st)
			}
			g1 := groupRecord(t, rep, "g1")
			if g1.Outcome != OutcomeNotExecuted {
				t.Fatalf("g1 Outcome = %v, want %v", g1.Outcome, OutcomeNotExecuted)
			}
		})
	}
}

func TestValidateUsesSnapshotTakenAtCallStart(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "agg", Priority: 0, ShortCircuit: false})
	rec := &execRecorder{}

	// h1 在执行期间注销 h2 并注册 h3：本次调用已开始，
	// 必须使用调用开始时刻的快照，h2 仍执行、h3 不执行。
	mutating := HookFunc(func(context.Context, Target) (Decision, error) {
		rec.mu.Lock()
		rec.calls = append(rec.calls, "h1")
		rec.mu.Unlock()
		// 该钩子会被多次调用（第二次 Validate），对重复变更容错。
		if _, err := r.Unregister("agg", "h2"); err != nil && !errors.Is(err, ErrHookNotFound) {
			return Allow, err
		}
		_, err := r.Register(HookRegistration{Group: "agg", ID: "h3", Hook: rec.hook("h3", Allow, nil)})
		if errors.Is(err, ErrHookExists) {
			err = nil
		}
		return Allow, err
	})
	mustRegister(t, r, "agg", "h1", mutating)
	mustRegister(t, r, "agg", "h2", rec.hook("h2", Allow, nil))

	rep, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "Y"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := fmt.Sprint(rec.called()); got != "[h1 h2]" {
		t.Fatalf("executed = %v, want [h1 h2] (snapshot taken at call start)", got)
	}
	g := groupRecord(t, rep, "agg")
	if len(g.Hooks) != 2 {
		t.Fatalf("report hooks = %d, want 2 (snapshot must be consistent within one call)", len(g.Hooks))
	}

	// 下一次调用使用新快照。
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()
	if _, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "Y"}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := fmt.Sprint(rec.called()); got != "[h1 h3]" {
		t.Fatalf("second call executed = %v, want [h1 h3]", got)
	}
}

func TestValidateReportsSnapshotVersion(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})
	v1 := mustRegister(t, r, "g", "a", allowAll())

	rep, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "Z"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rep.SnapshotVersion != v1 {
		t.Fatalf("SnapshotVersion = %d, want %d", rep.SnapshotVersion, v1)
	}

	v2 := mustRegister(t, r, "g", "b", allowAll())
	rep2, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "Z"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rep2.SnapshotVersion != v2 || rep2.SnapshotVersion == rep.SnapshotVersion {
		t.Fatalf("SnapshotVersion = %d, want %d (monotonic)", rep2.SnapshotVersion, v2)
	}
}

func TestSkippedCountIndependentOfHistory(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r,
		GroupSpec{Name: "sc", Priority: 0, ShortCircuit: true},
		GroupSpec{Name: "low", Priority: 1, ShortCircuit: true},
	)

	// 制造大量注册/注销历史。
	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("tmp-%d", i)
		mustRegister(t, r, "sc", id, allowAll())
		if _, err := r.Unregister("sc", id); err != nil {
			t.Fatalf("Unregister: %v", err)
		}
	}

	rec := &execRecorder{}
	mustRegister(t, r, "sc", "deny", rec.hook("deny", Reject, nil))
	mustRegister(t, r, "sc", "after", rec.hook("after", Allow, nil))
	mustRegister(t, r, "low", "l1", rec.hook("l1", Allow, nil))

	rep, err := r.Validate(context.Background(), Target{Kind: "objectType", Name: "H"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// 报告规模只与本次快照的 3 个钩子相关：1 执行 + 2 跳过。
	total := 0
	for _, g := range rep.Groups {
		total += len(g.Hooks)
	}
	if total != 3 {
		t.Fatalf("report hook records = %d, want 3 (snapshot size only)", total)
	}
	if got := rep.SkippedCount(); got != 2 {
		t.Fatalf("SkippedCount = %d, want 2", got)
	}
	if got := fmt.Sprint(rec.called()); got != "[deny]" {
		t.Fatalf("executed = %v, want [deny]", got)
	}
}

func TestHookErrorMessage(t *testing.T) {
	err := &HookError{Group: "g", HookID: "h", Err: errBoom}
	if !strings.Contains(err.Error(), `"h"`) || !strings.Contains(err.Error(), `"g"`) {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !errors.Is(err, errBoom) {
		t.Fatal("Unwrap should expose cause")
	}
}
