package lifecycle

import (
	"testing"
	"time"
)

// 两类查询身份对同一对象四种状态的可见性组合。
func TestIdentityVisibilityMatrix(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)
	clock.Set(t0)

	mustCreate(t, svc, nil, "alive")

	mustCreate(t, svc, nil, "grace")
	if err := svc.Delete("grace", IdentityAdmin, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	mustCreate(t, svc, nil, "frozen")
	if err := svc.Freeze("frozen", IdentityAdmin, time.Hour); err != nil {
		t.Fatal(err)
	}

	mustCreate(t, svc, nil, "archived")
	if err := svc.Freeze("archived", IdentityAdmin, time.Second); err != nil {
		t.Fatal(err)
	}
	clock.Set(t0.Add(2 * time.Second))
	if err := svc.Archive("archived", IdentityAdmin); err != nil {
		t.Fatal(err)
	}
	clock.Set(t0)

	cases := []struct {
		id                string
		userVisible       bool
		adminVisible      bool
		adminSeesAttrs    bool
		adminSeesFreezeDL bool
		state             State
	}{
		{"alive", true, true, true, false, StateAlive},
		{"grace", false, true, true, false, StateGrace},
		{"frozen", false, true, false, true, StateFrozen},
		{"archived", false, true, true, false, StateArchived},
	}
	for _, tc := range cases {
		uv := svc.GetView(tc.id, IdentityUser)
		if uv.Exists != true || uv.Visible != tc.userVisible {
			t.Errorf("%s user: exists=%v visible=%v, want visible=%v",
				tc.id, uv.Exists, uv.Visible, tc.userVisible)
		}
		if tc.userVisible {
			if uv.Attrs["name"] != tc.id {
				t.Errorf("%s user lost attrs", tc.id)
			}
		} else if uv.Attrs != nil {
			t.Errorf("%s user must not receive attrs", tc.id)
		}

		av := svc.GetView(tc.id, IdentityAdmin)
		if !av.Visible || av.State != tc.state {
			t.Errorf("%s admin: visible=%v state=%s want state=%s",
				tc.id, av.Visible, av.State, tc.state)
		}
		if tc.adminSeesAttrs {
			if av.Attrs["name"] != tc.id {
				t.Errorf("%s admin should see attrs, got %v", tc.id, av.Attrs)
			}
		} else if av.Attrs != nil {
			t.Errorf("%s admin must not see attrs, got %v", tc.id, av.Attrs)
		}
		if tc.adminSeesFreezeDL {
			if av.FreezeDeadline.IsZero() {
				t.Errorf("%s admin must see freeze deadline", tc.id)
			}
		}
	}

	// 不存在对象：两类身份都得到 Exists=false、Visible=false。
	for _, idn := range []Identity{IdentityUser, IdentityAdmin} {
		v := svc.GetView("missing", idn)
		if v.Exists || v.Visible {
			t.Fatalf("missing object view = %+v", v)
		}
	}
}

// 出边可见性完全跟随源对象；链接自身不携带删除状态。
func TestEdgeVisibilityFollowsSource(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)
	clock.Set(t0)
	mustCreate(t, svc, nil, "src")
	mustCreate(t, svc, nil, "dst")
	if err := svc.AddEdge(Edge{ID: "e1", SourceID: "src", TargetID: "dst"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddEdge(Edge{ID: "e2", SourceID: "src", TargetID: "dst"}); err != nil {
		t.Fatal(err)
	}

	expectEdges := func(actor Identity, wantN int) {
		t.Helper()
		got := svc.ViewEdges("src", actor)
		if len(got) != wantN {
			t.Fatalf("edges for %v = %d, want %d", actor, len(got), wantN)
		}
	}

	expectEdges(IdentityUser, 2)
	expectEdges(IdentityAdmin, 2)

	// 存活 → 宽限：一般使用者立刻看不到边，管理员仍可见。
	if err := svc.Delete("src", IdentityAdmin, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if es := svc.ViewEdges("src", IdentityUser); len(es) != 0 {
		t.Fatalf("user edges during grace = %d, want 0", len(es))
	}
	if es := svc.ViewEdges("src", IdentityAdmin); len(es) != 2 {
		t.Fatalf("admin edges during grace = %d, want 2", len(es))
	}

	// 撤销恢复：一般使用者重新可见同一组边。
	if err := svc.Undo("src", IdentityAdmin); err != nil {
		t.Fatal(err)
	}
	expectEdges(IdentityUser, 2)

	// 冻结：一般使用者不可见；管理员可见边（结构存在）但拿不到源对象属性。
	if err := svc.Freeze("src", IdentityAdmin, time.Hour); err != nil {
		t.Fatal(err)
	}
	if es := svc.ViewEdges("src", IdentityUser); len(es) != 0 {
		t.Fatalf("user edges frozen = %d", len(es))
	}
	if es := svc.ViewEdges("src", IdentityAdmin); len(es) != 2 {
		t.Fatalf("admin edges frozen = %d, want 2", len(es))
	}
	av := svc.GetView("src", IdentityAdmin)
	if av.Attrs != nil || av.FreezeDeadline.IsZero() {
		t.Fatalf("admin frozen view = %+v", av)
	}

	// 期满归档：一般使用者仍不可见；管理员可见但源为已归档。
	clock.Set(t0.Add(2 * time.Hour))
	if es := svc.ViewEdges("src", IdentityUser); len(es) != 0 {
		t.Fatalf("user edges archived = %d", len(es))
	}
	if es := svc.ViewEdges("src", IdentityAdmin); len(es) != 2 {
		t.Fatalf("admin edges archived = %d, want 2", len(es))
	}

	// 不存在的源对象：无错误、无边。
	if es := svc.ViewEdges("ghost", IdentityAdmin); es != nil {
		t.Fatalf("edges of ghost = %v", es)
	}
}
