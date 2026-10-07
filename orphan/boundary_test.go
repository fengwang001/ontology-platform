package orphan

import "testing"

// 公共场景：v1 规定对象类型 X 必需链接类型 L，宽限 5。
// 对象 o 在 t=10 创建，必需链接 k 在 t=12 建立、t=20 撤销，
// 因此 v1 下 zeroSince=20，orphanDue=25。
// v2 在 t=30 调整（内容按用例变化），retro 可为 true/false。
func baseStore(t *testing.T, v2Spec RuleSpec, retro bool) *Store {
	t.Helper()
	v1 := RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}
	s := NewStore(v1, 0)
	err := s.Append(
		Event{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		Event{ID: "p1", Kind: EvObjectCreated, Object: "p", ObjectType: "Y", Time: 10},
		Event{ID: "l1", Kind: EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 12},
		Event{ID: "r1", Kind: EvLinkRevoked, Link: "k", Time: 20},
	)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := s.AdjustRule(v2Spec, retro, 30); err != nil {
		t.Fatalf("adjust: %v", err)
	}
	return s
}

func mustStatus(t *testing.T, s *Store, at Time, version int) Status {
	t.Helper()
	det, err := s.Determine(Query{Object: "o", At: at, Version: version})
	if err != nil {
		t.Fatalf("determine(at=%d,v=%d): %v", at, version, err)
	}
	return det.Status
}

func mustErrCode(t *testing.T, s *Store, at Time, version int) ErrorCode {
	t.Helper()
	_, err := s.Determine(Query{Object: "o", At: at, Version: version})
	if err == nil {
		t.Fatalf("determine(at=%d,v=%d): expected error", at, version)
	}
	return err.(*Error).Code
}

// 追溯适用：v2 追溯作废 v1，治理全部历史时刻。
func TestRetroactiveAdjustmentGovernsAllHistory(t *testing.T) {
	v2 := RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{}, GracePeriod: 5}
	s := baseStore(t, v2, true)

	for _, at := range []Time{15, 25, 35} {
		if got := mustStatus(t, s, at, 2); got != StatusNotOrphan {
			t.Fatalf("at=%d declared=v2: got %v, want NotOrphan", at, got)
		}
		if got := mustErrCode(t, s, at, 1); got != ErrRuleVersionVoided {
			t.Fatalf("at=%d declared=v1: got %v, want RuleVersionVoided", at, got)
		}
	}
}

// 非追溯适用：v1 治理 t<30，v2 治理 t>=30，互不越界。
func TestNonRetroactiveAdjustmentPartitionsTimeline(t *testing.T) {
	v2 := RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{}, GracePeriod: 5}
	s := baseStore(t, v2, false)

	cases := []struct {
		at      Time
		version int
		want    Status
		wantErr ErrorCode
	}{
		{15, 1, StatusNotOrphan, ErrNone},              // 链接尚未撤销
		{25, 1, StatusRetroOrphanDormant, ErrNone},     // orphanDue=25 恰好在边界
		{35, 1, StatusNotOrphan, ErrRuleVersionVoided}, // v1 不再治理 t=35
		{15, 2, StatusNotOrphan, ErrRuleVersionVoided}, // v2 不追溯
		{25, 2, StatusNotOrphan, ErrRuleVersionVoided},
		{35, 2, StatusNotOrphan, ErrNone}, // v2 下 L 非必需
	}
	for _, c := range cases {
		if c.wantErr != ErrNone {
			if got := mustErrCode(t, s, c.at, c.version); got != c.wantErr {
				t.Fatalf("at=%d v=%d: err %v, want %v", c.at, c.version, got, c.wantErr)
			}
			continue
		}
		if got := mustStatus(t, s, c.at, c.version); got != c.want {
			t.Fatalf("at=%d v=%d: %v, want %v", c.at, c.version, got, c.want)
		}
	}
}

// 宽限期边界：orphanDue=25，t=24 不孤，t=25 孤，t=26 孤。
func TestGracePeriodBoundary(t *testing.T) {
	s := NewStore(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}, 0)
	if err := s.Append(
		Event{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		Event{ID: "p1", Kind: EvObjectCreated, Object: "p", ObjectType: "Y", Time: 10},
		Event{ID: "l1", Kind: EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 12},
		Event{ID: "r1", Kind: EvLinkRevoked, Link: "k", Time: 20},
	); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   Time
		want Status
	}{
		{19, StatusNotOrphan},
		{24, StatusNotOrphan},
		{25, StatusRetroOrphanDormant},
		{26, StatusRetroOrphanDormant},
	}
	for _, c := range cases {
		if got := mustStatus(t, s, c.at, 1); got != c.want {
			t.Fatalf("at=%d: %v, want %v", c.at, got, c.want)
		}
	}
}

// 归零后重新建立必需链接会中断孤儿区间；再次撤销以新区间起算。
func TestRelinkResetsZeroStretch(t *testing.T) {
	s := NewStore(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}, 0)
	if err := s.Append(
		Event{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		Event{ID: "p1", Kind: EvObjectCreated, Object: "p", ObjectType: "Y", Time: 10},
		Event{ID: "l1", Kind: EvLinkCreated, Link: "k1", LinkType: "L", From: "o", To: "p", Time: 12},
		Event{ID: "r1", Kind: EvLinkRevoked, Link: "k1", Time: 20},
		Event{ID: "l2", Kind: EvLinkCreated, Link: "k2", LinkType: "L", From: "o", To: "p", Time: 22},
		Event{ID: "r2", Kind: EvLinkRevoked, Link: "k2", Time: 26},
	); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   Time
		want Status
	}{
		{21, StatusNotOrphan},          // 处于归零区间但宽限未满（due=25）
		{22, StatusNotOrphan},          // k2 建立，区间中断
		{30, StatusNotOrphan},          // 新 due=31
		{31, StatusRetroOrphanDormant}, // 26+5
	}
	for _, c := range cases {
		if got := mustStatus(t, s, c.at, 1); got != c.want {
			t.Fatalf("at=%d: %v, want %v", c.at, got, c.want)
		}
	}
}

// 从未建立过必需链接的对象不会成为孤儿（避免空真）。
func TestNeverHadRequiredLinkIsNotOrphan(t *testing.T) {
	s := NewStore(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}, 0)
	if err := s.Append(
		Event{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
	); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, 100, 1); got != StatusNotOrphan {
		t.Fatalf("got %v, want NotOrphan", got)
	}
}

// 追溯版本新增必需性：历史上本无孤儿义务的对象被追溯判定为孤儿。
func TestRetroactiveAdditionOfRequirement(t *testing.T) {
	s := NewStore(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{}, GracePeriod: 5}, 0)
	if err := s.Append(
		Event{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		Event{ID: "p1", Kind: EvObjectCreated, Object: "p", ObjectType: "Y", Time: 10},
		Event{ID: "l1", Kind: EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 12},
		Event{ID: "r1", Kind: EvLinkRevoked, Link: "k", Time: 20},
	); err != nil {
		t.Fatal(err)
	}
	v2 := RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}
	if _, err := s.AdjustRule(v2, true, 30); err != nil {
		t.Fatal(err)
	}
	det, err := s.Determine(Query{Object: "o", At: 25, Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != StatusRetroOrphanDormant {
		t.Fatalf("got %v, want RetroOrphanDormant", det.Status)
	}
	if det.OrphanDue != 25 || det.ZeroSince != 20 {
		t.Fatalf("zeroSince=%d due=%d, want 20/25", det.ZeroSince, det.OrphanDue)
	}
}

// 幂等性：同一 (At, Version) 重复判定结果一致；非追溯调整不影响旧版本对旧时刻的判定。
func TestDeterminationIdempotentAcrossAdjustments(t *testing.T) {
	v1 := RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}
	s := NewStore(v1, 0)
	if err := s.Append(
		Event{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		Event{ID: "p1", Kind: EvObjectCreated, Object: "p", ObjectType: "Y", Time: 10},
		Event{ID: "l1", Kind: EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 12},
		Event{ID: "r1", Kind: EvLinkRevoked, Link: "k", Time: 20},
	); err != nil {
		t.Fatal(err)
	}
	q := Query{Object: "o", At: 25, Version: 1}
	first, err := s.Determine(q)
	if err != nil {
		t.Fatal(err)
	}
	// 非追溯调整发生在 t=30 之后，不影响 v1 对 t=25 的治理。
	if _, err := s.AdjustRule(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{}, GracePeriod: 1}, false, 30); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := s.Determine(q)
		if err != nil {
			t.Fatal(err)
		}
		if again.Status != first.Status || again.OrphanDue != first.OrphanDue || again.ZeroSince != first.ZeroSince {
			t.Fatalf("run %d differs: %+v vs %+v", i, again, first)
		}
	}
}
