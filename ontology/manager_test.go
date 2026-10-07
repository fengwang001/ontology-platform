package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// setupType 注册一个链接类型及 n 个源对象、n 个目标对象。
func setupType(t *testing.T, m *Manager, typeID string, srcLimit, tgtLimit, n int) {
	t.Helper()
	if err := m.DefineLinkType(typeID, "A", "B", srcLimit, tgtLimit); err != nil {
		t.Fatalf("DefineLinkType: %v", err)
	}
	for i := 0; i < n; i++ {
		m.RegisterObject(fmt.Sprintf("s%d", i))
		m.RegisterObject(fmt.Sprintf("t%d", i))
	}
}

// createN 从源对象 src 向 n 个不同目标各建一条链接，返回链接 ID（按创建序）。
func createN(t *testing.T, m *Manager, typeID, src string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-l%d", src, i)
		if err := m.CreateLink(typeID, id, src, fmt.Sprintf("t%d", i)); err != nil {
			t.Fatalf("CreateLink %s: %v", id, err)
		}
		ids = append(ids, id)
	}
	return ids
}

// pendingIDs 返回某方向登记组中处于待处理状态的链接 ID（升序）。
func pendingIDs(m *Manager, typeID, obj string) []string {
	var out []string
	for _, v := range m.QueryLinks(typeID, DirectionOut, obj) {
		if v.Status == StatusPending {
			out = append(out, v.ID)
		}
	}
	return out
}

// activeIDs 返回某方向登记组中有效链接的 ID（升序）。
func activeIDs(m *Manager, typeID, obj string) []string {
	var out []string
	for _, v := range m.QueryLinks(typeID, DirectionOut, obj) {
		if v.Status == StatusActive {
			out = append(out, v.ID)
		}
	}
	return out
}

// TestDeterministicMarking 验证下调时超额选择是确定的：
// 总是选择 (Seq, ID) 最新的若干条，且同样输入重复执行结果完全相同。
func TestDeterministicMarking(t *testing.T) {
	run := func() ([]string, []string) {
		m := NewManager()
		setupType(t, m, "T", 10, Unlimited, 8)
		ids := createN(t, m, "T", "s0", 5)
		if err := m.SetLimit("T", DirectionOut, 3); err != nil {
			t.Fatalf("SetLimit: %v", err)
		}
		return pendingIDs(m, "T", "s0"), ids
	}
	p1, ids := run()
	p2, _ := run()
	want := ids[len(ids)-2:] // 最新创建的两条被标记
	if !reflect.DeepEqual(p1, want) {
		t.Fatalf("pending = %v, want newest %v", p1, want)
	}
	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("not deterministic: %v vs %v", p1, p2)
	}
}

// TestAlternatingAdjustments 多次连续下调与上调交替后，
// 核对待处理集合与有效集合的最终状态。
func TestAlternatingAdjustments(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 10, Unlimited, 12)
	ids := createN(t, m, "T", "s0", 10) // ids[0] 最旧，ids[9] 最新

	steps := []struct {
		limit       int
		wantPending []string
	}{
		{6, ids[6:]},     // 10 -> 6：最新 4 条待处理
		{8, ids[8:]},     // 6 -> 8：恢复 2 条
		{3, ids[3:]},     // 8 -> 3：最新 7 条待处理
		{5, ids[5:]},     // 3 -> 5：恢复 2 条
		{10, nil},        // 5 -> 10：全部恢复
		{4, ids[4:]},     // 10 -> 4
		{Unlimited, nil}, // 取消上限：全部恢复
		{2, ids[2:]},     // 重新收紧到 2：最新 8 条待处理
	}
	for i, st := range steps {
		if err := m.SetLimit("T", DirectionOut, st.limit); err != nil {
			t.Fatalf("step %d SetLimit(%d): %v", i, st.limit, err)
		}
		got := pendingIDs(m, "T", "s0")
		if !reflect.DeepEqual(got, st.wantPending) {
			t.Fatalf("step %d limit=%d: pending=%v, want %v", i, st.limit, got, st.wantPending)
		}
		stats := m.Stats("T", DirectionOut, "s0")
		if stats.Active+stats.Pending != 10 {
			t.Fatalf("step %d: active+pending=%d, want 10", i, stats.Active+stats.Pending)
		}
		if stats.TotalRegistered != 10 {
			t.Fatalf("step %d: totalRegistered=%d, want 10", i, stats.TotalRegistered)
		}
	}
}

// TestRestoreOrderReversed 验证恢复顺序与标记顺序相反，
// 且下调再上调后与从未发生过调整完全等价。
func TestRestoreOrderReversed(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 10, Unlimited, 8)
	ids := createN(t, m, "T", "s0", 5)

	if err := m.SetLimit("T", DirectionOut, 2); err != nil {
		t.Fatal(err)
	}
	if err := m.SetLimit("T", DirectionOut, 10); err != nil {
		t.Fatal(err)
	}
	var marked, restored []string
	for _, ev := range m.AuditLog() {
		switch ev.Kind {
		case EventExcessMarked:
			marked = append(marked, ev.LinkID)
		case EventPendingRestored:
			restored = append(restored, ev.LinkID)
		}
	}
	wantMarked := []string{ids[4], ids[3], ids[2]} // 最新优先标记
	if !reflect.DeepEqual(marked, wantMarked) {
		t.Fatalf("marked order = %v, want %v", marked, wantMarked)
	}
	wantRestored := []string{ids[2], ids[3], ids[4]} // 严格逆序恢复
	if !reflect.DeepEqual(restored, wantRestored) {
		t.Fatalf("restored order = %v, want %v", restored, wantRestored)
	}
	// 与从未发生过下调再上调完全等价。
	fresh := NewManager()
	setupType(t, fresh, "T", 10, Unlimited, 8)
	createN(t, fresh, "T", "s0", 5)
	for i, id := range ids {
		got, err := m.GetLink(id)
		if err != nil {
			t.Fatal(err)
		}
		want, err := fresh.GetLink(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want.Status || got.MarkBasis != want.MarkBasis {
			t.Fatalf("link %d (%s): got %+v, want equivalent to %+v", i, id, got, want)
		}
	}
	if got := m.Stats("T", DirectionOut, "s0"); got.Active != 5 || got.Pending != 0 {
		t.Fatalf("stats = %+v, want 5 active 0 pending", got)
	}
}

// TestTrajectoryIndependence 待处理期间上限多次反复调整时，
// 最终状态必须与从最初状态直接跳到最后一次上限的单次调整完全等价。
func TestTrajectoryIndependence(t *testing.T) {
	build := func(limits ...int) *Manager {
		m := NewManager()
		setupType(t, m, "T", 10, Unlimited, 12)
		createN(t, m, "T", "s0", 10)
		for _, l := range limits {
			if err := m.SetLimit("T", DirectionOut, l); err != nil {
				t.Fatalf("SetLimit(%d): %v", l, err)
			}
		}
		return m
	}
	multi := build(4, 9, 2, 7, 3, 6) // 多次反复调整
	direct := build(6)               // 直接跳到最后一次上限
	if got, want := pendingIDs(multi, "T", "s0"), pendingIDs(direct, "T", "s0"); !reflect.DeepEqual(got, want) {
		t.Fatalf("pending: multi=%v direct=%v", got, want)
	}
	if got, want := activeIDs(multi, "T", "s0"), activeIDs(direct, "T", "s0"); !reflect.DeepEqual(got, want) {
		t.Fatalf("active: multi=%v direct=%v", got, want)
	}
	gs, ds := multi.Stats("T", DirectionOut, "s0"), direct.Stats("T", DirectionOut, "s0")
	if gs.Active != ds.Active || gs.Pending != ds.Pending || gs.EffectiveLimit != ds.EffectiveLimit {
		t.Fatalf("stats differ: multi=%+v direct=%+v", gs, ds)
	}
}

// TestKeepRaisesEffectiveLimit 显式保留动作校验通过后将链接恢复有效，
// 并相应提升该方向的有效上限。
func TestKeepRaisesEffectiveLimit(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 3, Unlimited, 8)
	ids := createN(t, m, "T", "s0", 3)
	if err := m.SetLimit("T", DirectionOut, 2); err != nil {
		t.Fatal(err)
	}
	if got := pendingIDs(m, "T", "s0"); !reflect.DeepEqual(got, ids[2:]) {
		t.Fatalf("pending = %v, want %v", got, ids[2:])
	}
	// 非待处理链接不能执行保留。
	if err := m.ResolvePending(ids[0], ResolveKeep); !errors.Is(err, ErrNotPending) {
		t.Fatalf("keep active link: err = %v, want ErrNotPending", err)
	}
	// 保留最新一条：有效上限应从 2 提升到 3。
	if err := m.ResolvePending(ids[2], ResolveKeep); err != nil {
		t.Fatalf("ResolveKeep: %v", err)
	}
	stats := m.Stats("T", DirectionOut, "s0")
	if stats.EffectiveLimit != 3 || stats.Active != 3 || stats.Pending != 0 {
		t.Fatalf("stats = %+v, want active=3 pending=0 effLimit=3", stats)
	}
	v, _ := m.GetLink(ids[2])
	if v.Status != StatusActive {
		t.Fatalf("kept link status = %v", v.Status)
	}
	// 有效上限已提升：基础上限再次下调到 2 时不再产生超额
	// （对照组中同样的下调会标记 1 条待处理）。
	if err := m.SetLimit("T", DirectionOut, 2); err != nil {
		t.Fatal(err)
	}
	if got := pendingIDs(m, "T", "s0"); len(got) != 0 {
		t.Fatalf("after keep, re-decrease marked %v, want none (effective limit raised)", got)
	}
	ctrl := NewManager()
	setupType(t, ctrl, "T", 3, Unlimited, 8)
	createN(t, ctrl, "T", "s0", 3)
	if err := ctrl.SetLimit("T", DirectionOut, 2); err != nil {
		t.Fatal(err)
	}
	if got := pendingIDs(ctrl, "T", "s0"); len(got) != 1 {
		t.Fatalf("control: pending = %v, want 1", got)
	}
	// 有效数等于提升后的有效上限：新建仍被拒绝。
	m.RegisterObject("t-extra")
	if err := m.CreateLink("T", "extra", "s0", "t-extra"); !errors.Is(err, ErrLimitFull) {
		t.Fatalf("create at raised-but-full limit: err = %v, want ErrLimitFull", err)
	}
}

// TestRevokeForcesDelete 对象被撤销后，待处理链接无法转为保留，
// 且撤销判定优先于默认处理路径：默认清理直接强制删除。
func TestRevokeForcesDelete(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 3, Unlimited, 8)
	ids := createN(t, m, "T", "s0", 3)
	if err := m.SetLimit("T", DirectionOut, 1); err != nil {
		t.Fatal(err)
	}
	// ids[1] 目标对象被撤销 -> 只能删除；ids[2] 走一般规则 -> 删除（仍超额）。
	m.RevokeObject("t1")
	if err := m.ResolvePending(ids[1], ResolveKeep); !errors.Is(err, ErrObjectRevoked) {
		t.Fatalf("keep with revoked endpoint: err = %v, want ErrObjectRevoked", err)
	}
	if err := m.FinalizePending(ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := m.FinalizePending(ids[2]); err != nil {
		t.Fatal(err)
	}
	var kinds []EventKind
	for _, ev := range m.AuditLog() {
		if ev.Kind == EventPendingForceDeleted || ev.Kind == EventPendingDeleted {
			kinds = append(kinds, ev.Kind)
		}
	}
	want := []EventKind{EventPendingForceDeleted, EventPendingForceDeleted, EventPendingDeleted, EventPendingDeleted}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("delete events = %v, want %v (每条链接两个方向各记一条)", kinds, want)
	}
	v1, _ := m.GetLink(ids[1])
	v2, _ := m.GetLink(ids[2])
	if v1.Status != StatusDeleted || v2.Status != StatusDeleted {
		t.Fatalf("statuses = %v, %v, want both deleted", v1.Status, v2.Status)
	}
	if got := m.Stats("T", DirectionOut, "s0"); got.Active != 1 || got.Pending != 0 || got.TotalRegistered != 3 {
		t.Fatalf("stats = %+v, want active=1 pending=0 total=3", got)
	}
}

// TestPendingVisibilityAndStats 待处理链接对查询可见、计入历史统计，
// 但不参与基数判断（新建按有效链接数判定）。
func TestPendingVisibilityAndStats(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 4, Unlimited, 8)
	ids := createN(t, m, "T", "s0", 4)
	if err := m.SetLimit("T", DirectionOut, 3); err != nil {
		t.Fatal(err)
	}
	views := m.QueryLinks("T", DirectionOut, "s0")
	if len(views) != 4 {
		t.Fatalf("query returned %d links, want 4 (pending 仍然可见)", len(views))
	}
	if views[3].Status != StatusPending || views[3].ID != ids[3] {
		t.Fatalf("last view = %+v, want pending %s", views[3], ids[3])
	}
	stats := m.Stats("T", DirectionOut, "s0")
	if stats.Active != 3 || stats.Pending != 1 || stats.TotalRegistered != 4 {
		t.Fatalf("stats = %+v, want active=3 pending=1 total=4", stats)
	}
	// 有效数已达上限 3：新建被拒绝并记录 EventCreateRejected。
	m.RegisterObject("t-x")
	if err := m.CreateLink("T", "x", "s0", "t-x"); !errors.Is(err, ErrLimitFull) {
		t.Fatalf("create at full limit: err = %v, want ErrLimitFull", err)
	}
	var rejected bool
	for _, ev := range m.AuditLog() {
		if ev.Kind == EventCreateRejected && ev.LinkID == "x" {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("missing EventCreateRejected audit record")
	}
}

// TestAuditCompleteness 每次处理都记录触发原因、标记依据与最终去向。
func TestAuditCompleteness(t *testing.T) {
	m := NewManager()
	setupType(t, m, "T", 3, Unlimited, 8)
	ids := createN(t, m, "T", "s0", 3)
	if err := m.SetLimit("T", DirectionOut, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.ResolvePending(ids[2], ResolveKeep); err != nil {
		t.Fatal(err)
	}
	if err := m.FinalizePending(ids[1]); err != nil {
		t.Fatal(err)
	}
	var marked, kept, deleted int
	for _, ev := range m.AuditLog() {
		switch ev.Kind {
		case EventExcessMarked, EventPendingKept, EventPendingDeleted, EventPendingRestored, EventPendingForceDeleted:
			if ev.Trigger == "" || ev.Disposition == "" {
				t.Fatalf("event %+v missing trigger/disposition", ev)
			}
		}
		switch ev.Kind {
		case EventExcessMarked:
			marked++
			if ev.Basis == "" {
				t.Fatalf("excess event missing basis: %+v", ev)
			}
		case EventPendingKept:
			kept++
		case EventPendingDeleted:
			deleted++
		}
	}
	if marked == 0 || kept == 0 || deleted == 0 {
		t.Fatalf("audit counts marked=%d kept=%d deleted=%d, want all > 0", marked, kept, deleted)
	}
}
