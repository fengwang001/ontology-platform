package dbscan

import (
	"reflect"
	"testing"
)

func snapCluster(ids ...int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestBuildEventsSevenTypes(t *testing.T) {
	// Birth：只含新簇
	ev := buildEvents(clusterSnapshot{}, clusterSnapshot{7: snapCluster(7)})
	if !reflect.DeepEqual(ev, []Event{{Type: EventBirth, OldLabels: nil, NewLabels: []int64{7}}}) {
		t.Fatalf("birth: %v", ev)
	}
	// Death：只含旧簇
	ev = buildEvents(clusterSnapshot{7: snapCluster(7)}, clusterSnapshot{})
	if !reflect.DeepEqual(ev, []Event{{Type: EventDeath, OldLabels: []int64{7}, NewLabels: nil}}) {
		t.Fatalf("death: %v", ev)
	}
	// 无事件：1 旧 1 新同标签（成员增减）
	before := clusterSnapshot{2: snapCluster(2, 3)}
	after := clusterSnapshot{2: snapCluster(2, 3, 4)}
	if ev := buildEvents(before, after); len(ev) != 0 {
		t.Fatalf("same-label membership change must yield no event: %v", ev)
	}
	// Relabel：1 旧 1 新，标签不同（共有核心点 3 前后均为核心）
	before = clusterSnapshot{2: snapCluster(2, 3)}
	after = clusterSnapshot{3: snapCluster(3)}
	ev = buildEvents(before, after)
	if !reflect.DeepEqual(ev, []Event{{Type: EventRelabel, OldLabels: []int64{2}, NewLabels: []int64{3}}}) {
		t.Fatalf("relabel: %v", ev)
	}
	// Merge：2 旧 1 新
	before = clusterSnapshot{2: snapCluster(2, 4), 8: snapCluster(8, 9)}
	after = clusterSnapshot{2: snapCluster(2, 4, 8, 9)}
	ev = buildEvents(before, after)
	if !reflect.DeepEqual(ev, []Event{{Type: EventMerge, OldLabels: []int64{2, 8}, NewLabels: []int64{2}}}) {
		t.Fatalf("merge: %v", ev)
	}
	// Split：1 旧 2 新
	before = clusterSnapshot{2: snapCluster(2, 4, 8, 9)}
	after = clusterSnapshot{2: snapCluster(2, 4), 8: snapCluster(8, 9)}
	ev = buildEvents(before, after)
	if !reflect.DeepEqual(ev, []Event{{Type: EventSplit, OldLabels: []int64{2}, NewLabels: []int64{2, 8}}}) {
		t.Fatalf("split: %v", ev)
	}
	// Reshape：2 旧 2 新（同一操作里一边分裂一边并入）
	before = clusterSnapshot{
		2: snapCluster(2, 3),
		8: snapCluster(8, 9),
	}
	after = clusterSnapshot{
		3: snapCluster(3, 8),
		9: snapCluster(9, 2),
	}
	ev = buildEvents(before, after)
	if len(ev) != 1 || ev[0].Type != EventReshape ||
		!reflect.DeepEqual(ev[0].OldLabels, []int64{2, 8}) ||
		!reflect.DeepEqual(ev[0].NewLabels, []int64{3, 9}) {
		t.Fatalf("reshape: %v", ev)
	}
	// 事件按涉及的最小标签升序：Death(1)、Death(2)、Birth(20)。
	before = clusterSnapshot{1: snapCluster(1), 2: snapCluster(2)}
	after = clusterSnapshot{20: snapCluster(20)}
	ev = buildEvents(before, after)
	want := []Event{
		{Type: EventDeath, OldLabels: []int64{1}, NewLabels: nil},
		{Type: EventDeath, OldLabels: []int64{2}, NewLabels: nil},
		{Type: EventBirth, OldLabels: nil, NewLabels: []int64{20}},
	}
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("ordering: %v want %v", ev, want)
	}
}

// TestSharedCoreMustSurvive 验证“共有核心点”必须前后都存活且为核心：
// 旧簇核心 2,3；操作后 3 存活但不再是核心，则两边不连边（Death + Birth）。
func TestSharedCoreMustSurvive(t *testing.T) {
	before := clusterSnapshot{2: snapCluster(2, 3)}
	after := clusterSnapshot{5: snapCluster(5)} // 3 不在新簇核心集
	ev := buildEvents(before, after)
	if len(ev) != 2 {
		t.Fatalf("want Death+Birth when no shared core, got %v", ev)
	}
	types := map[EventType]bool{ev[0].Type: true, ev[1].Type: true}
	if !types[EventDeath] || !types[EventBirth] {
		t.Fatalf("want Death and Birth, got %v", ev)
	}
}

// TestRelabelOnRemoval：最小核心点被删除，其余核心仍相连 -> Relabel。
func TestRelabelOnRemoval(t *testing.T) {
	s, _ := New(5, 2, 100, 100)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 1, 0)
	mustInsert(t, s, 3, 2, 0) // 三个核心同簇，标签 1
	r, err := s.Remove(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Events) != 1 || r.Events[0].Type != EventRelabel ||
		!reflect.DeepEqual(r.Events[0].OldLabels, []int64{1}) ||
		!reflect.DeepEqual(r.Events[0].NewLabels, []int64{2}) {
		t.Fatalf("want Relabel 1->2, got %v", r.Events)
	}
	if lab, _ := s.Label(2); lab != 2 {
		t.Fatalf("label 2=%d", lab)
	}
}

// TestSplitNewLabelsAreMinCores：删除使簇分裂，新标签各取最小核心。
func TestSplitNewLabelsAreMinCores(t *testing.T) {
	// eps=2,minPts=2：相邻（间距<=2）的两点互为核心。
	// 直线链 1,2,3,4,5，间距 2；删除中间点 3 后两簇标签 1 和 4。
	s, _ := New(2, 2, 100, 100)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 2, 0)
	mustInsert(t, s, 3, 4, 0)
	mustInsert(t, s, 4, 6, 0)
	mustInsert(t, s, 5, 8, 0)
	if lab, _ := s.Label(5); lab != 1 {
		t.Fatalf("precondition label 5=%d want 1", lab)
	}
	r, err := s.Remove(3)
	if err != nil {
		t.Fatal(err)
	}
	var split *Event
	for i := range r.Events {
		if r.Events[i].Type == EventSplit {
			split = &r.Events[i]
		}
	}
	if split == nil {
		t.Fatalf("want Split, got %v (changes=%v)", r.Events, r.Changes)
	}
	if !reflect.DeepEqual(split.OldLabels, []int64{1}) ||
		!reflect.DeepEqual(split.NewLabels, []int64{1, 4}) {
		t.Fatalf("split labels=%v,%v want [1],[1 4]", split.OldLabels, split.NewLabels)
	}
	if lab, _ := s.Label(1); lab != 1 {
		t.Fatalf("label1=%d", lab)
	}
	if lab, _ := s.Label(4); lab != 4 {
		t.Fatalf("label4=%d want 4", lab)
	}
	if lab, _ := s.Label(5); lab != 4 {
		t.Fatalf("label5=%d want 4", lab)
	}
}

// TestReplayDeterminism：相同操作序列重放，报告与标签完全相同。
func TestReplayDeterminism(t *testing.T) {
	params := [4]int64{3, 2, 6, 30}
	ops := []string{
		"I 1 0 0", "I 2 3 0", "I 3 6 0", "T 3",
		"I 4 9 0", "I 5 12 0", "R 3", "T 7",
		"I 3 0 3", "T 20",
	}
	run := func() ([]*Result, map[int64]int64) {
		svc, _ := New(params[0], int(params[1]), params[2], int(params[3]))
		var rs []*Result
		for _, op := range ops {
			r, err := invoke(svc, op)
			if err != nil {
				t.Fatalf("op %s: %v", op, err)
			}
			rs = append(rs, r)
		}
		labels := make(map[int64]int64)
		for id := range svc.points {
			labels[id] = svc.label[id]
		}
		return rs, labels
	}
	r1, l1 := run()
	r2, l2 := run()
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("replay reports differ\n%v\n%v", r1, r2)
	}
	if !reflect.DeepEqual(l1, l2) {
		t.Fatalf("replay labels differ %v %v", l1, l2)
	}
}
