package activator

import (
	"errors"
	"testing"

	"ontology/config"
)

func mkPush(cu []string, ru []config.Route, cd, rd []string) config.Push {
	return config.Push{ClusterUpserts: cu, RouteUpserts: ru, ClusterDeletes: cd, RouteDeletes: rd}
}

func r1(name string, refs ...string) config.Route { return config.Route{Name: name, Refs: refs} }

func mustPush(t *testing.T, a *Activator, ver int64, p config.Push, now int64) {
	t.Helper()
	if err := a.Push(ver, p, now); err != nil {
		t.Fatalf("Push ver=%d now=%d unexpected err=%v", ver, now, err)
	}
}

func mustReady(t *testing.T, a *Activator, name string, now int64) {
	t.Helper()
	if err := a.Ready(name, now); err != nil {
		t.Fatalf("Ready(%s,%d) unexpected err=%v", name, now, err)
	}
}

func servingQ(t *testing.T, a *Activator, name string, now int64) (int64, bool) {
	t.Helper()
	v, ok, err := a.Serving(name, now)
	if err != nil {
		t.Fatalf("Serving(%s,%d) unexpected err=%v", name, now, err)
	}
	return v, ok
}

func wantErr(t *testing.T, a *Activator, want error, fn func() error) {
	t.Helper()
	if err := fn(); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

func TestSpecExampleThreePaths(t *testing.T) {
	setup := func() *Activator {
		a := New(10)
		mustPush(t, a, 1, mkPush([]string{"c1"}, []config.Route{r1("r1", "c1")}, nil, nil), 0)
		mustReady(t, a, "c1", 4)
		mustPush(t, a, 2, mkPush([]string{"c1", "c2"}, []config.Route{r1("r1", "c1", "c2")}, nil, nil), 5)
		return a
	}

	if v, ok := servingQ(t, setup(), "r1", 5); v != 1 || !ok {
		t.Fatalf("base serving want 1,true got %d,%v", v, ok)
	}

	// 路径甲：c2 就绪不切换，c1 就绪后切换 v2；之后删 c1 报在用。
	a := setup()
	mustReady(t, a, "c2", 7)
	if v, ok := servingQ(t, a, "r1", 7); v != 1 || !ok {
		t.Fatalf("path A mid want 1, got %d,%v", v, ok)
	}
	mustReady(t, a, "c1", 9)
	if v, ok := servingQ(t, a, "r1", 9); v != 2 || !ok {
		t.Fatalf("path A end want 2, got %d,%v", v, ok)
	}
	wantErr(t, a, config.ErrInUse, func() error {
		return a.Push(3, mkPush(nil, nil, []string{"c1"}, nil), 10)
	})

	// 路径乙：仅 c2 就绪；c1 超时保留旧在役，r1 激活 v2。
	a = setup()
	mustReady(t, a, "c2", 7)
	if v, ok := servingQ(t, a, "r1", 15); v != 2 || !ok {
		t.Fatalf("path B want 2, got %d,%v", v, ok)
	}

	// 路径丙：仅 c1 就绪；c2 无在役被移除，v2 丢弃，仍为 v1。
	a = setup()
	mustReady(t, a, "c1", 9)
	if v, ok := servingQ(t, a, "r1", 15); v != 1 || !ok {
		t.Fatalf("path C want 1, got %d,%v", v, ok)
	}
	st, err := a.State("c2", 15)
	if err != nil || st.Present {
		t.Fatalf("path C c2 should be removed, st=%+v err=%v", st, err)
	}
}

type edgeCase struct {
	name string
	run  func(t *testing.T, a *Activator)
}

func TestEdgeCases(t *testing.T) {
	cases := []edgeCase{
		{"version not contiguous", edgeVersion},
		{"dangling before in-use", edgeDanglingBeforeInUse},
		{"same resource updated and deleted", edgeUpdateAndDelete},
		{"duplicates empty and ref bounds", edgeParams},
		{"time invalid precedes rewind", edgeTime},
		{"exact timeout and one-ms margin", edgeTimeoutBoundary},
		{"new cluster timeout removes with pending", edgeNewClusterFails},
		{"update fail keeps old and activates", edgeUpdateFailActivates},
		{"same-instant timeout ordering", edgeSameInstantOrder},
		{"repeat push replaces pending and resets warming", edgeRepeatPush},
		{"partial readiness no switch", edgePartialReadiness},
		{"route delete immediate", edgeRouteDelete},
		{"rejected op persists nothing", edgeNackPersistsNothing},
		{"delete nonexistent is no-op", edgeDeleteMissingNoop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, New(10))
		})
	}
}

func edgeVersion(t *testing.T, a *Activator) {
	wantErr(t, a, config.ErrBadVersion, func() error {
		return a.Push(2, mkPush([]string{"c"}, nil, nil, nil), 0)
	})
	mustPush(t, a, 1, mkPush([]string{"c"}, nil, nil, nil), 0)
	wantErr(t, a, config.ErrBadVersion, func() error {
		return a.Push(3, mkPush([]string{"c"}, nil, nil, nil), 1)
	})
	if a.lastAccepted != 1 || a.maxNow != 0 {
		t.Fatalf("state mutated by NACK: ver=%d now=%d", a.lastAccepted, a.maxNow)
	}
}

func edgeDanglingBeforeInUse(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1"}, []config.Route{r1("r1", "c1")}, nil, nil), 0)
	mustReady(t, a, "c1", 1)
	err := a.Push(2, mkPush(nil, []config.Route{r1("r1", "cx")}, []string{"c1"}, nil), 2)
	if !errors.Is(err, config.ErrDanglingRef) {
		t.Fatalf("want dangling first, got %v", err)
	}
}

func edgeUpdateAndDelete(t *testing.T, a *Activator) {
	wantErr(t, a, config.ErrInvalidParam, func() error {
		return a.Push(1, mkPush([]string{"c1"}, nil, []string{"c1"}, nil), 0)
	})
	wantErr(t, a, config.ErrInvalidParam, func() error {
		return a.Push(1, mkPush(nil, []config.Route{r1("r1", "c1")}, nil, []string{"r1"}), 0)
	})
}

func edgeParams(t *testing.T, a *Activator) {
	bad := []config.Push{
		mkPush([]string{""}, nil, nil, nil),
		mkPush([]string{"c", "c"}, nil, nil, nil),
		mkPush(nil, []config.Route{{Name: "r"}}, nil, nil),
		mkPush(nil, []config.Route{r1("r", "c", "c")}, nil, nil),
		mkPush(nil, []config.Route{{Name: "r", Refs: []string{"c", ""}}}, nil, nil),
		mkPush(nil, nil, nil, []string{""}),
		mkPush(nil, nil, []string{"c", "c"}, nil),
		mkPush(nil, []config.Route{r1("r", "c"), r1("r", "c")}, nil, nil),
	}
	for i, p := range bad {
		if err := a.Push(1, p, 0); !errors.Is(err, config.ErrInvalidParam) {
			t.Fatalf("case %d want invalid, got %v", i, err)
		}
	}
	refs := make([]string, 9)
	for i := range refs {
		refs[i] = "c"
	}
	wantErr(t, a, config.ErrInvalidParam, func() error {
		return a.Push(1, mkPush(nil, []config.Route{{Name: "r", Refs: refs}}, nil, nil), 0)
	})
}

func edgeTime(t *testing.T, a *Activator) {
	wantErr(t, a, config.ErrInvalidTime, func() error {
		return a.Push(1, mkPush([]string{"c"}, nil, nil, nil), -1)
	})
	mustPush(t, a, 1, mkPush([]string{"c"}, nil, nil, nil), 5)
	wantErr(t, a, config.ErrClockRewind, func() error {
		return a.Push(2, mkPush([]string{"c"}, nil, nil, nil), 4)
	})
	if a.maxNow != 5 {
		t.Fatalf("maxNow mutated by NACK: %d", a.maxNow)
	}
}

func edgeTimeoutBoundary(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1"}, []config.Route{r1("r1", "c1")}, nil, nil), 0)
	mustReady(t, a, "c1", 9) // 差 1 仍可 Ready
	if v, ok := servingQ(t, a, "r1", 9); !ok || v != 1 {
		t.Fatalf("r1 want 1, got %d,%v", v, ok)
	}
	// 独立实例：since=20，恰在 30 失败，新集群被移除。
	b := New(10)
	mustPush(t, b, 1, mkPush([]string{"c1"}, nil, nil, nil), 20)
	wantErr(t, b, config.ErrNotWarming, func() error { return b.Ready("c1", 30) })
	st, err := b.State("c1", 30)
	if err != nil || st.Present || st.HasWarming {
		t.Fatalf("c1 should be removed at exact timeout, st=%+v err=%v", st, err)
	}
}

func edgeNewClusterFails(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1", "c2"},
		[]config.Route{r1("r1", "c1"), r1("r2", "c1", "c2")}, nil, nil), 0)
	mustReady(t, a, "c1", 1)
	if v, ok := servingQ(t, a, "r1", 1); !ok || v != 1 {
		t.Fatalf("r1 want 1, got %d,%v", v, ok)
	}
	if _, ok := servingQ(t, a, "r2", 10); ok {
		t.Fatalf("r2 must never serve after c2 timeout")
	}
	st, _ := a.State("c2", 10)
	if st.Present {
		t.Fatalf("c2 should be removed")
	}
}

func edgeUpdateFailActivates(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1", "c2"}, []config.Route{r1("r1", "c1", "c2")}, nil, nil), 0)
	mustReady(t, a, "c1", 1)
	mustReady(t, a, "c2", 1)
	mustPush(t, a, 2, mkPush([]string{"c1"}, []config.Route{r1("r1", "c1", "c2")}, nil, nil), 5)
	if v, ok := servingQ(t, a, "r1", 15); !ok || v != 2 {
		t.Fatalf("r1 want 2 after fallback activation, got %d,%v", v, ok)
	}
	st, _ := a.State("c1", 15)
	if !st.Present || st.HasWarming || st.Version != 1 {
		t.Fatalf("c1 keeps serving v1, st=%+v", st)
	}
}

func edgeSameInstantOrder(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"b", "a"},
		[]config.Route{r1("rb", "b"), r1("ra", "a")}, nil, nil), 0)
	if _, ok := servingQ(t, a, "ra", 10); ok {
		t.Fatalf("ra must not serve")
	}
	if _, ok := servingQ(t, a, "rb", 10); ok {
		t.Fatalf("rb must not serve")
	}
	stA, _ := a.State("a", 10)
	stB, _ := a.State("b", 10)
	if stA.Present || stB.Present {
		t.Fatalf("both new clusters should be removed, a=%+v b=%+v", stA, stB)
	}
	// 已消失的集群再删除视为无操作，推送应被接受。
	mustPush(t, a, 2, mkPush(nil, nil, []string{"a", "b", "missing"}, nil), 10)
}

func edgeRepeatPush(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1", "c2"}, []config.Route{r1("r1", "c1")}, nil, nil), 0)
	// 重复推送：r1 待激活改为 [c1,c2]，v1 待激活被取代；c1 重新预热 since=5。
	mustPush(t, a, 2, mkPush([]string{"c1"}, []config.Route{r1("r1", "c1", "c2")}, nil, nil), 5)
	st, _ := a.State("c1", 5)
	if !st.HasWarming || st.Since != 5 || st.WarmingVersion != 2 {
		t.Fatalf("c1 warming reset want since=5 v2, st=%+v", st)
	}
	if a.pendRefCount["c1"] != 1 || a.pendRefCount["c2"] != 1 {
		t.Fatalf("pendRefCount replaced wrong: %v", a.pendRefCount)
	}
	if _, ok := servingQ(t, a, "r1", 5); ok {
		t.Fatalf("r1 must not serve before readiness")
	}
	mustReady(t, a, "c1", 6)
	mustReady(t, a, "c2", 6)
	if v, ok := servingQ(t, a, "r1", 6); !ok || v != 2 {
		t.Fatalf("r1 want 2, got %d,%v", v, ok)
	}
}

func edgePartialReadiness(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1", "c2"}, []config.Route{r1("r1", "c1", "c2")}, nil, nil), 0)
	mustReady(t, a, "c1", 1)
	if _, ok := servingQ(t, a, "r1", 1); ok {
		t.Fatalf("r1 must not switch with c2 warming")
	}
	st, _ := a.State("c2", 1)
	if !st.Present || !st.HasWarming {
		t.Fatalf("c2 still warming, st=%+v", st)
	}
}

func edgeRouteDelete(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1", "c2"},
		[]config.Route{r1("r1", "c1"), r1("r2", "c1", "c2")}, nil, nil), 0)
	mustReady(t, a, "c1", 1)
	// r1 在役、r2 待激活；删除 r2 立即生效，之后删 c2 不再被引用。
	mustPush(t, a, 2, mkPush(nil, nil, []string{"c2"}, []string{"r2"}), 2)
	if _, ok := servingQ(t, a, "r2", 2); ok {
		t.Fatalf("r2 deleted must be absent")
	}
	st, _ := a.State("c2", 2)
	if st.Present {
		t.Fatalf("c2 deleted must be absent")
	}
	// 在役 r1 仍可服务。
	if v, ok := servingQ(t, a, "r1", 2); !ok || v != 1 {
		t.Fatalf("r1 want 1, got %d,%v", v, ok)
	}
}

func edgeNackPersistsNothing(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush([]string{"c1"}, []config.Route{r1("r1", "c1")}, nil, nil), 0)
	mustReady(t, a, "c1", 4)
	// ver=2 更新 c1，since=5。
	mustPush(t, a, 2, mkPush([]string{"c1", "c2"}, []config.Route{r1("r1", "c1", "c2")}, nil, nil), 5)
	before := a.dump()
	// 非法序号 + 大 now 的 NACK：不得结算（c1 的预热不能因此失败）。
	err := a.Push(99, mkPush([]string{"c1"}, nil, nil, nil), 100)
	if !errors.Is(err, config.ErrBadVersion) {
		t.Fatalf("want bad version, got %v", err)
	}
	if a.maxNow != 5 {
		t.Fatalf("maxNow changed by NACK: %d", a.maxNow)
	}
	if got := a.dump(); got != before {
		t.Fatalf("state changed by rejected settle:\nbefore=%s\nafter =%s", before, got)
	}
	// Ready 未在预热的 NACK 同样不落盘 now。
	if err := a.Ready("ghost", 100); !errors.Is(err, config.ErrNotWarming) {
		t.Fatalf("want not warming, got %v", err)
	}
	if a.maxNow != 5 {
		t.Fatalf("Ready NACK advanced now: %d", a.maxNow)
	}
}

func edgeDeleteMissingNoop(t *testing.T, a *Activator) {
	mustPush(t, a, 1, mkPush(nil, nil, []string{"nope"}, []string{"ghost"}), 0)
	if a.lastAccepted != 1 {
		t.Fatalf("no-op deletes should be accepted")
	}
}
