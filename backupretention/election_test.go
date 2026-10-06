package backupretention

import "testing"

func TestDailyElectionTieAndOrdering(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("aaa", 1, t0))
	mustOK(t, s.RegisterFull("bbb", 1, t0)) // 同时刻，字典序较大者当选
	mustOK(t, s.RegisterFull("early", 1, t0+daySeconds+10))
	mustOK(t, s.RegisterFull("late", 1, t0+daySeconds+20))

	mustOK(t, s.SetPolicy(Policy{Daily: 2}, t0+daySeconds+20))
	p, err := s.Plan(t0 + daySeconds + 20)
	mustOK(t, err)
	idx := planIndex(p)
	b := assertKept(t, idx, "bbb")
	if !hasLayer(b.Reason, LayerDaily) {
		t.Fatal("bbb should be day-0 representative")
	}
	assertKept(t, idx, "late")
	assertDeletable(t, idx, "aaa")
	assertDeletable(t, idx, "early")

	for i := 1; i < len(p.Retained); i++ {
		prev, cur := p.Retained[i-1], p.Retained[i]
		if prev.CreatedAt > cur.CreatedAt ||
			(prev.CreatedAt == cur.CreatedAt && prev.ID > cur.ID) {
			t.Fatalf("retained not uniquely ordered: %v before %v", prev, cur)
		}
	}
}

func TestEmptyCurrentPeriodStillConsumesSlot(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("old", 1, t0))
	// N=2，当前周期（第 1 天）为空，窗口仅第 0、1 天；old 在第 0 天仍当选。
	mustOK(t, s.SetPolicy(Policy{Daily: 2}, t0+daySeconds))
	p, err := s.Plan(t0 + daySeconds + 20)
	mustOK(t, err)
	idx := planIndex(p)
	assertKept(t, idx, "old")

	// N=1 时窗口只剩空的当前周期：old 无代表，可删除（时刻继续推进）。
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, t0+daySeconds+21))
	p, err = s.Plan(t0 + daySeconds + 21)
	mustOK(t, err)
	idx = planIndex(p)
	assertDeletable(t, idx, "old")
}

func TestWeeklyAcrossYearMonth(t *testing.T) {
	s := NewService()
	// 2024-01-01 周一，2024-01-05 同周；2024-02-01 是另一月另一周。
	jan5 := mon20240101 + 4*daySeconds
	feb1 := int64(1706745600)
	mustOK(t, s.RegisterFull("wk1", 1, mon20240101))
	mustOK(t, s.RegisterFull("wk1late", 1, jan5))
	mustOK(t, s.RegisterFull("feb", 1, feb1))

	// Weekly=1 且当前周（含 2024-01-29 周）没有备份 -> 无周代表。
	mustOK(t, s.SetPolicy(Policy{Weekly: 1, Monthly: 2}, feb1))
	p, err := s.Plan(feb1)
	mustOK(t, err)
	idx := planIndex(p)
	assertDeletable(t, idx, "wk1")
	assertKept(t, idx, "feb")
	b := assertKept(t, idx, "wk1late") // 1 月代表
	if !hasLayer(b.Reason, LayerMonthly) {
		t.Fatal("wk1late should be January monthly representative")
	}

	// 用周期序号精确决定需要的 N：跨 1 月末到 2 月初的周序号差 +1。
	gap := periodOf(LayerWeekly, feb1+1) - periodOf(LayerWeekly, jan5) + 1
	mustOK(t, s.SetPolicy(Policy{Weekly: int(gap)}, feb1+1))
	p, err = s.Plan(feb1 + 1)
	mustOK(t, err)
	idx = planIndex(p)
	b = assertKept(t, idx, "wk1late")
	if !hasLayer(b.Reason, LayerWeekly) {
		t.Fatal("wk1late should be weekly representative within 5-week window")
	}
}

func TestCorruptionSkipsNextAndWholePeriodEmpty(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("good", 1, t0+10))
	mustOK(t, s.RegisterFull("bad", 1, t0+20))
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, t0+20))

	// 最晚者损坏 -> 顺延次晚者。
	mustOK(t, s.MarkCorrupt("bad", t0+30))
	p, err := s.Plan(t0 + 30)
	mustOK(t, err)
	idx := planIndex(p)
	assertDeletable(t, idx, "bad")
	assertKept(t, idx, "good")

	// 全部损坏 -> 整周期无代表。
	mustOK(t, s.MarkCorrupt("good", t0+40))
	p, err = s.Plan(t0 + 40)
	mustOK(t, err)
	idx = planIndex(p)
	assertDeletable(t, idx, "bad")
	assertDeletable(t, idx, "good")

	// 重复标记不是错误。
	mustOK(t, s.MarkCorrupt("bad", t0+50))
}

func TestAncestorCorruptPoisonsChain(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("root", 1, t0))
	mustOK(t, s.RegisterIncremental("mid", 1, t0+10, "root"))
	mustOK(t, s.RegisterIncremental("leaf", 1, t0+20, "mid"))
	mustOK(t, s.SetPolicy(Policy{Daily: 100}, t0+20))

	p, err := s.Plan(t0 + 20)
	mustOK(t, err)
	idx := planIndex(p)
	assertKept(t, idx, "leaf")
	assertKept(t, idx, "mid")
	assertKept(t, idx, "root")

	mustOK(t, s.MarkCorrupt("root", t0+30))
	p, err = s.Plan(t0 + 30)
	mustOK(t, err)
	idx = planIndex(p)
	assertDeletable(t, idx, "root")
	assertDeletable(t, idx, "mid")
	assertDeletable(t, idx, "leaf")
}

func TestDependencyOnlyReasonDistinct(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("root", 1, t0))
	mustOK(t, s.RegisterIncremental("leaf", 1, t0+10, "root"))
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, t0+10))

	p, err := s.Plan(t0 + 10)
	mustOK(t, err)
	idx := planIndex(p)
	root := assertKept(t, idx, "root")
	if !root.Reason.Dependency || len(root.Reason.DirectLayers) != 0 || root.Reason.LegalHold {
		t.Fatalf("root should be dependency-only: %+v", root.Reason)
	}
	leaf := assertKept(t, idx, "leaf")
	if !hasLayer(leaf.Reason, LayerDaily) || leaf.Reason.Dependency {
		t.Fatalf("leaf should be direct-only: %+v", leaf.Reason)
	}
}

func TestLegalHoldOnCorruptAndReasonsCombine(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("root", 1, t0))
	mustOK(t, s.RegisterIncremental("leaf", 1, t0+10, "root"))
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, t0+10))

	// leaf 同时具备：直接保留（损坏前）、法律保留；root 仅依赖保护。
	mustOK(t, s.SetLegalHold("leaf", t0+15, true))
	p, err := s.Plan(t0 + 15)
	mustOK(t, err)
	idx := planIndex(p)
	leaf := assertKept(t, idx, "leaf")
	if !leaf.Reason.LegalHold || !hasLayer(leaf.Reason, LayerDaily) {
		t.Fatalf("leaf should have direct+legal: %+v", leaf.Reason)
	}

	// root 损坏 -> leaf 不可恢复，直接资格丧失；但法律保留仍保住 leaf，
	// root 因被法律保留的 leaf 依赖而必须保留（损坏也保留），仅依赖原因。
	mustOK(t, s.MarkCorrupt("root", t0+20))
	p, err = s.Plan(t0 + 20)
	mustOK(t, err)
	idx = planIndex(p)
	leaf = assertKept(t, idx, "leaf")
	if !leaf.Reason.LegalHold || hasLayer(leaf.Reason, LayerDaily) {
		t.Fatalf("leaf should be legal-only after ancestor corruption: %+v", leaf.Reason)
	}
	root := assertKept(t, idx, "root")
	if !root.Reason.Dependency || root.Reason.LegalHold {
		t.Fatalf("corrupt root must stay via dependency: %+v", root.Reason)
	}

	// 解除 leaf 法律保留 -> 链上全部可删。
	mustOK(t, s.SetLegalHold("leaf", t0+25, false))
	p, err = s.Plan(t0 + 25)
	mustOK(t, err)
	idx = planIndex(p)
	assertDeletable(t, idx, "root")
	assertDeletable(t, idx, "leaf")
}

func TestLegalHoldAncestorsRetained(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("root", 1, t0))
	mustOK(t, s.RegisterIncremental("mid", 1, t0+10, "root"))
	mustOK(t, s.RegisterIncremental("leaf", 1, t0+20, "mid"))
	// 无任何保留策略；只对 leaf 设置法律保留。
	mustOK(t, s.SetLegalHold("leaf", t0+30, true))
	p, err := s.Plan(t0 + 30)
	mustOK(t, err)
	idx := planIndex(p)
	leaf := assertKept(t, idx, "leaf")
	if !leaf.Reason.LegalHold || leaf.Reason.Dependency {
		t.Fatalf("leaf legal-only: %+v", leaf.Reason)
	}
	mid := assertKept(t, idx, "mid")
	if !mid.Reason.Dependency || mid.Reason.LegalHold {
		t.Fatalf("mid dependency-only (ancestor of legal): %+v", mid.Reason)
	}
	root := assertKept(t, idx, "root")
	if !root.Reason.Dependency {
		t.Fatalf("root must be retained as ancestor of legal-held leaf")
	}
}
