package ontology

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func allowAll() Hook {
	return HookFunc(func(context.Context, Target) (Decision, error) { return Allow, nil })
}

// execRecorder 记录钩子的实际执行顺序，用于断言执行痕迹。
type execRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *execRecorder) hook(id string, decision Decision, err error) Hook {
	return HookFunc(func(context.Context, Target) (Decision, error) {
		r.mu.Lock()
		r.calls = append(r.calls, id)
		r.mu.Unlock()
		return decision, err
	})
}

func (r *execRecorder) called() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func mustDeclare(t *testing.T, r *Registry, specs ...GroupSpec) {
	t.Helper()
	for _, s := range specs {
		if err := r.DeclareGroup(s); err != nil {
			t.Fatalf("DeclareGroup(%s): %v", s.Name, err)
		}
	}
}

func mustRegister(t *testing.T, r *Registry, group, id string, h Hook) uint64 {
	t.Helper()
	v, err := r.Register(HookRegistration{Group: group, ID: id, Hook: h})
	if err != nil {
		t.Fatalf("Register(%s/%s): %v", group, id, err)
	}
	return v
}

func snapshotIDs(s Snapshot) map[string][]string {
	out := make(map[string][]string, len(s.Groups))
	for _, g := range s.Groups {
		ids := make([]string, 0, len(g.Hooks))
		for _, h := range g.Hooks {
			ids = append(ids, h.ID)
		}
		out[g.Spec.Name] = ids
	}
	return out
}

func snapshotGroupOrder(s Snapshot) []string {
	names := make([]string, 0, len(s.Groups))
	for _, g := range s.Groups {
		names = append(names, g.Spec.Name)
	}
	return names
}

func TestDeclareGroupDuplicate(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})
	if err := r.DeclareGroup(GroupSpec{Name: "g", Priority: 1}); !errors.Is(err, ErrGroupExists) {
		t.Fatalf("want ErrGroupExists, got %v", err)
	}
}

func TestRegisterValidationErrors(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})

	if _, err := r.Register(HookRegistration{Group: "nope", ID: "h", Hook: allowAll()}); !errors.Is(err, ErrGroupNotDeclared) {
		t.Fatalf("want ErrGroupNotDeclared, got %v", err)
	}
	if _, err := r.Register(HookRegistration{Group: "g", ID: "", Hook: allowAll()}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("want ErrInvalidRegistration, got %v", err)
	}
	if _, err := r.Register(HookRegistration{Group: "g", ID: "h", Hook: nil}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("want ErrInvalidRegistration, got %v", err)
	}
	mustRegister(t, r, "g", "h", allowAll())
	if _, err := r.Register(HookRegistration{Group: "g", ID: "h", Hook: allowAll()}); !errors.Is(err, ErrHookExists) {
		t.Fatalf("want ErrHookExists, got %v", err)
	}
	if _, err := r.Unregister("g", "missing"); !errors.Is(err, ErrHookNotFound) {
		t.Fatalf("want ErrHookNotFound, got %v", err)
	}
	if _, err := r.Unregister("nope", "h"); !errors.Is(err, ErrHookNotFound) {
		t.Fatalf("want ErrHookNotFound, got %v", err)
	}
}

func TestSnapshotGroupPriorityOrder(t *testing.T) {
	r := NewRegistry()
	// 声明顺序与优先级无关，快照必须按声明的优先级排序。
	mustDeclare(t, r,
		GroupSpec{Name: "low", Priority: 30},
		GroupSpec{Name: "high", Priority: 10},
		GroupSpec{Name: "mid", Priority: 20},
		// 相同优先级按名称字典序，保证确定性。
		GroupSpec{Name: "tie-b", Priority: 20},
		GroupSpec{Name: "tie-a", Priority: 20},
	)
	for _, g := range []string{"low", "high", "mid", "tie-a", "tie-b"} {
		mustRegister(t, r, g, "h-"+g, allowAll())
	}
	got := snapshotGroupOrder(r.Snapshot())
	want := []string{"high", "mid", "tie-a", "tie-b", "low"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("group order = %v, want %v", got, want)
	}
}

func TestSnapshotHookRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})
	mustRegister(t, r, "g", "a", allowAll())
	mustRegister(t, r, "g", "b", allowAll())
	mustRegister(t, r, "g", "c", allowAll())

	got := snapshotIDs(r.Snapshot())["g"]
	want := []string{"a", "b", "c"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("hook order = %v, want %v", got, want)
	}
}

func TestUnregisterReregisterMovesToEnd(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})
	mustRegister(t, r, "g", "a", allowAll())
	mustRegister(t, r, "g", "b", allowAll())
	mustRegister(t, r, "g", "c", allowAll())

	if _, err := r.Unregister("g", "a"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	// 以相同分组与标识重新注册：视为新注册，排到组内末尾。
	mustRegister(t, r, "g", "a", allowAll())

	got := snapshotIDs(r.Snapshot())["g"]
	want := []string{"b", "c", "a"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("hook order after re-register = %v, want %v", got, want)
	}
}

func TestRegisterBatchPreservesIntraBatchOrder(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})
	mustRegister(t, r, "g", "before", allowAll())

	// 同一批次对外表现为单次动作：组内顺序按批次内声明的相对顺序。
	batch := []HookRegistration{
		{Group: "g", ID: "b1", Hook: allowAll()},
		{Group: "g", ID: "b2", Hook: allowAll()},
		{Group: "g", ID: "b3", Hook: allowAll()},
	}
	if _, err := r.RegisterBatch(batch); err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	mustRegister(t, r, "g", "after", allowAll())

	got := snapshotIDs(r.Snapshot())["g"]
	want := []string{"before", "b1", "b2", "b3", "after"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("hook order = %v, want %v", got, want)
	}
}

func TestRegisterBatchAtomic(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})
	mustRegister(t, r, "g", "existing", allowAll())

	// 批次内含非法项（重复标识 + 未声明分组）：整批不得生效。
	batch := []HookRegistration{
		{Group: "g", ID: "new-1", Hook: allowAll()},
		{Group: "g", ID: "existing", Hook: allowAll()},
		{Group: "ghost", ID: "new-2", Hook: allowAll()},
	}
	if _, err := r.RegisterBatch(batch); err == nil {
		t.Fatal("want error for invalid batch")
	}
	if got := snapshotIDs(r.Snapshot())["g"]; fmt.Sprint(got) != "[existing]" {
		t.Fatalf("invalid batch must not take effect, hooks = %v", got)
	}

	// 批次内自相重复同样整批拒绝。
	dup := []HookRegistration{
		{Group: "g", ID: "x", Hook: allowAll()},
		{Group: "g", ID: "x", Hook: allowAll()},
	}
	if _, err := r.RegisterBatch(dup); !errors.Is(err, ErrHookExists) {
		t.Fatalf("want ErrHookExists, got %v", err)
	}
	if got := snapshotIDs(r.Snapshot())["g"]; fmt.Sprint(got) != "[existing]" {
		t.Fatalf("duplicate batch must not take effect, hooks = %v", got)
	}
}

func TestSnapshotIsolationFromLaterMutations(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})
	mustRegister(t, r, "g", "a", allowAll())
	mustRegister(t, r, "g", "b", allowAll())

	snap := r.Snapshot()
	v := snap.Version

	// 快照提取后的注册/注销不得影响已提取的快照。
	if _, err := r.Unregister("g", "a"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	mustRegister(t, r, "g", "c", allowAll())

	if snap.Version != v {
		t.Fatalf("snapshot version mutated: %d -> %d", v, snap.Version)
	}
	got := snapshotIDs(snap)["g"]
	want := []string{"a", "b"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stale snapshot hooks = %v, want %v", got, want)
	}

	// 新快照反映最新状态。
	gotNow := snapshotIDs(r.Snapshot())["g"]
	wantNow := []string{"b", "c"}
	if fmt.Sprint(gotNow) != fmt.Sprint(wantNow) {
		t.Fatalf("fresh snapshot hooks = %v, want %v", gotNow, wantNow)
	}
}

func TestSnapshotCostIndependentOfHistory(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})

	// 制造大量注册/注销历史，最终只留 3 个存活钩子。
	const history = 5000
	for i := 0; i < history; i++ {
		id := fmt.Sprintf("tmp-%d", i)
		mustRegister(t, r, "g", id, allowAll())
		if _, err := r.Unregister("g", id); err != nil {
			t.Fatalf("Unregister(%s): %v", id, err)
		}
	}
	mustRegister(t, r, "g", "live-1", allowAll())
	mustRegister(t, r, "g", "live-2", allowAll())
	mustRegister(t, r, "g", "live-3", allowAll())

	snap := r.Snapshot()
	// 快照内容只与当前存活钩子相关，与历史操作总次数无关。
	if n := snap.HookCount(); n != 3 {
		t.Fatalf("HookCount = %d, want 3", n)
	}
	if got := snapshotIDs(snap)["g"]; fmt.Sprint(got) != "[live-1 live-2 live-3]" {
		t.Fatalf("live hooks = %v", got)
	}
}
