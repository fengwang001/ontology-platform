package backupretention

import "testing"

func TestZeroPolicyDeletesAll(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("F", 1, t0))
	mustOK(t, s.RegisterIncremental("I", 1, t0+10, "F"))
	// 默认策略三层均为 0，且无法律保留：无任何保留原因。
	p, err := s.Plan(t0 + 10)
	mustOK(t, err)
	if len(p.Retained) != 0 || len(p.Deletable) != 2 {
		t.Fatalf("zero policy: kept=%d deletable=%d", len(p.Retained), len(p.Deletable))
	}
}

func TestPolicyRelaxAndTighten(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	for i := 0; i < 6; i++ {
		id := string(rune('a' + i))
		mustOK(t, s.RegisterFull(id, 1, t0+int64(i)*daySeconds))
	}
	now := t0 + 5*daySeconds

	// 收紧：N=1 只保留最后一天。
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, now))
	p, err := s.Plan(now)
	mustOK(t, err)
	if len(p.Retained) != 1 || p.Retained[0].ID != "f" {
		t.Fatalf("tight policy kept=%v", p.Retained)
	}

	// 放宽：N=6 覆盖全部六个周期。
	mustOK(t, s.SetPolicy(Policy{Daily: 6}, now+1))
	p, err = s.Plan(now + 1)
	mustOK(t, err)
	if len(p.Retained) != 6 {
		t.Fatalf("relaxed policy kept=%d want 6", len(p.Retained))
	}

	// 再次收紧立即反映。
	mustOK(t, s.SetPolicy(Policy{Daily: 2}, now+2))
	p, err = s.Plan(now + 2)
	mustOK(t, err)
	if len(p.Retained) != 2 {
		t.Fatalf("re-tightened kept=%d want 2", len(p.Retained))
	}

	// 策略变更本身不删除任何备份：恢复宽策略后全部重现。
	mustOK(t, s.SetPolicy(Policy{Daily: 6}, now+3))
	p, err = s.Plan(now + 3)
	mustOK(t, err)
	if len(p.Retained) != 6 {
		t.Fatalf("policy change must not delete backups; kept=%d", len(p.Retained))
	}
}

func TestLayerCountUpperBoundAccepted(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("F", 1, t0))
	mustOK(t, s.SetPolicy(Policy{Daily: 1000, Weekly: 1000, Monthly: 1000}, t0))
	p, err := s.Plan(t0)
	mustOK(t, err)
	idx := planIndex(p)
	assertKept(t, idx, "F")
}

func TestCleanupIdempotentAndNoOrphans(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("F_old", 1, t0-30*daySeconds))
	mustOK(t, s.RegisterIncremental("I_old", 1, t0-30*daySeconds+10, "F_old"))
	mustOK(t, s.RegisterFull("F_keep", 1, t0))
	mustOK(t, s.RegisterIncremental("I_keep", 1, t0+10, "F_keep"))
	mustOK(t, s.SetPolicy(Policy{Daily: 2}, t0+10))

	first, err := s.Cleanup(t0 + 10)
	mustOK(t, err)
	deleted := map[string]bool{}
	for _, b := range first.Deletable {
		deleted[b.ID] = true
	}
	if !deleted["F_old"] || !deleted["I_old"] || deleted["F_keep"] || deleted["I_keep"] {
		t.Fatalf("first cleanup deleted set wrong: %v", deleted)
	}

	// 第二次（同一时刻再计划语义）必须不删除任何备份。
	second, err := s.Cleanup(t0 + 10)
	mustOK(t, err)
	if len(second.Deletable) != 0 {
		t.Fatalf("second cleanup must delete nothing, got %v", second.Deletable)
	}

	// 注册表中无孤儿：每个仍在的增量其父也在。
	for _, b := range s.reg.all() {
		if b.Kind == KindIncremental {
			if _, ok := s.reg.get(b.ParentID); !ok {
				t.Fatalf("orphan incremental %q after cleanup", b.ID)
			}
		}
	}

	// 删除后标识不再已登记。
	if _, ok := s.reg.get("F_old"); ok {
		t.Fatal("deleted id must no longer be registered")
	}
}

func TestReregisterSameIDAfterDelete(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("F", 1, t0))
	// 零策略下清理删除 F。
	_, err := s.Cleanup(t0)
	mustOK(t, err)
	if s.reg.len() != 0 {
		t.Fatalf("registry should be empty, len=%d", s.reg.len())
	}
	// 同一标识可重新登记为新备份。
	mustOK(t, s.RegisterFull("F", 2, t0+100))
	b, ok := s.reg.get("F")
	if !ok || b.Size != 2 || b.CreatedAt != t0+100 {
		t.Fatalf("re-registered backup mismatch: %+v", b)
	}
}

func TestCleanupCannotDeleteAncestorOfKept(t *testing.T) {
	s := NewService()
	t0 := mon20240101
	mustOK(t, s.RegisterFull("root", 1, t0))
	mustOK(t, s.RegisterIncremental("mid", 1, t0+10, "root"))
	mustOK(t, s.RegisterIncremental("leaf", 1, t0+20, "mid"))
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, t0+20))

	p, err := s.Cleanup(t0 + 20)
	mustOK(t, err)
	if len(p.Deletable) != 0 {
		t.Fatalf("dependency chain must protect root/mid: %v", p.Deletable)
	}
	if s.reg.len() != 3 {
		t.Fatalf("nothing should be deleted, len=%d", s.reg.len())
	}
}

func TestDeterministicReplay(t *testing.T) {
	// 相同操作序列重放，计划与原因完全相同。
	build := func() *Plan {
		s := NewService()
		t0 := mon20240101
		mustOK(t, s.RegisterFull("b", 1, t0))
		mustOK(t, s.RegisterFull("a", 1, t0))
		mustOK(t, s.RegisterIncremental("c", 1, t0+5, "b"))
		mustOK(t, s.SetPolicy(Policy{Daily: 1}, t0+5))
		mustOK(t, s.MarkCorrupt("a", t0+6))
		p, err := s.Plan(t0 + 7)
		mustOK(t, err)
		return p
	}
	p1, p2 := build(), build()
	if plansFingerprint(p1) != plansFingerprint(p2) {
		t.Fatal("replayed plans differ")
	}
}

func plansFingerprint(p *Plan) string {
	out := ""
	for _, b := range p.Retained {
		out += "K:" + b.ID + ":" + layersString(b.Reason.DirectLayers) +
			boolChar(b.Reason.Dependency) + boolChar(b.Reason.LegalHold) + ";"
	}
	for _, b := range p.Deletable {
		out += "D:" + b.ID + ";"
	}
	return out
}

func boolChar(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
