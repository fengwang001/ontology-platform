package orphan

import "testing"

// 构造：v1 空规则，v2 在 t=30 追溯新增必需性（L 必需，宽限 5）。
// o 的必需链接 t=12 建立、t=20 撤销 → 追溯下 orphanDue=25。
func retroStore(t *testing.T, extra ...Event) *Store {
	t.Helper()
	s := NewStore(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{}, GracePeriod: 5}, 0)
	base := []Event{
		{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		{ID: "lt2", Kind: EvLinkTypeCreated, LinkType: "M", Time: 0},
		{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		{ID: "p1", Kind: EvObjectCreated, Object: "p", ObjectType: "Y", Time: 10},
		{ID: "l1", Kind: EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 12},
		{ID: "r1", Kind: EvLinkRevoked, Link: "k", Time: 20},
	}
	if err := s.Append(append(base, extra...)...); err != nil {
		t.Fatal(err)
	}
	v2 := RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}
	if _, err := s.AdjustRule(v2, true, 30); err != nil {
		t.Fatal(err)
	}
	return s
}

// 追溯孤儿且后续无活动：呈现为 RetroOrphanDormant。
func TestRetroOrphanDormantPresentation(t *testing.T) {
	s := retroStore(t)
	det, err := s.Determine(Query{Object: "o", At: 40, Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != StatusRetroOrphanDormant {
		t.Fatalf("got %v, want RetroOrphanDormant", det.Status)
	}
	if len(det.Activities) != 0 {
		t.Fatalf("unexpected activities: %+v", det.Activities)
	}
}

// 追溯孤儿但后续仍有正常活动：呈现为 RetroOrphanWithActivity 并列出活动，
// 不得与真正被级联清理的孤儿混同。
func TestRetroOrphanWithActivityPresentation(t *testing.T) {
	s := retroStore(t,
		Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "name", Value: "x", Time: 27},
		Event{ID: "l2", Kind: EvLinkCreated, Link: "k2", LinkType: "M", From: "o", To: "p", Time: 33},
	)
	det, err := s.Determine(Query{Object: "o", At: 40, Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != StatusRetroOrphanWithActivity {
		t.Fatalf("got %v, want RetroOrphanWithActivity", det.Status)
	}
	if len(det.Activities) != 2 {
		t.Fatalf("got %d activities, want 2: %+v", len(det.Activities), det.Activities)
	}
	if det.Activities[0].Time != 27 || det.Activities[1].Time != 33 {
		t.Fatalf("activity times: %+v", det.Activities)
	}
}

// 恰好落在 orphanDue 时刻的活动不算"之后"。
func TestActivityExactlyAtDueIsNotPostOrphan(t *testing.T) {
	s := retroStore(t,
		Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "name", Value: "x", Time: 25},
	)
	det, err := s.Determine(Query{Object: "o", At: 40, Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != StatusRetroOrphanDormant {
		t.Fatalf("got %v, want RetroOrphanDormant", det.Status)
	}
}

// 事件流中存在明确级联标记：呈现为 OrphanConfirmed，区别于追溯孤儿。
func TestConfirmedOrphanPresentation(t *testing.T) {
	s := retroStore(t,
		Event{ID: "mk1", Kind: EvObjectMarkedOrphan, Object: "o", Time: 26},
	)
	det, err := s.Determine(Query{Object: "o", At: 40, Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != StatusOrphanConfirmed {
		t.Fatalf("got %v, want OrphanConfirmed", det.Status)
	}
	if det.ConfirmedAt != 26 {
		t.Fatalf("ConfirmedAt=%d, want 26", det.ConfirmedAt)
	}
}

// 归零区间之前的孤儿标记不构成确认。
func TestMarkBeforeZeroStretchIsNotConfirmation(t *testing.T) {
	s := retroStore(t,
		Event{ID: "mk1", Kind: EvObjectMarkedOrphan, Object: "o", Time: 15},
	)
	det, err := s.Determine(Query{Object: "o", At: 40, Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != StatusRetroOrphanDormant {
		t.Fatalf("got %v, want RetroOrphanDormant", det.Status)
	}
}

// 网络重建输出：对象属性、链接活性与孤儿标记来自事件流本身。
func TestRebuildNetworkReflectsEventStream(t *testing.T) {
	s := retroStore(t,
		Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "name", Value: "x", Time: 27},
		Event{ID: "mk1", Kind: EvObjectMarkedOrphan, Object: "p", Time: 28},
	)
	st, err := s.RebuildNetwork(40)
	if err != nil {
		t.Fatal(err)
	}
	o := st.Objects["o"]
	if o.Properties["name"] != "x" || o.MarkedOrphan {
		t.Fatalf("object o state: %+v", o)
	}
	if !st.Objects["p"].MarkedOrphan {
		t.Fatalf("object p should be marked orphan")
	}
	if st.Links["k"].Active {
		t.Fatalf("link k should be inactive")
	}
	// 在撤销之前的时刻重建：链接应为活跃。
	st15, err := s.RebuildNetwork(15)
	if err != nil {
		t.Fatal(err)
	}
	if !st15.Links["k"].Active {
		t.Fatalf("link k should be active at t=15")
	}
}
