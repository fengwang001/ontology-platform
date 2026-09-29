package scheduler

import "testing"

func TestFullMinZoneBlocksOthers(t *testing.T) {
	// 区 a 唯一节点容量 1；区 b、c 容量 3。S=1。
	nodes := []Node{
		{ID: "n-a", Zone: "a", Slots: 1, Labels: map[string]string{"k": "v"}},
		{ID: "n-b", Zone: "b", Slots: 3, Labels: map[string]string{"k": "v"}},
		{ID: "n-c", Zone: "c", Slots: 3, Labels: map[string]string{"k": "v"}},
	}
	s := newTestScheduler(t, Config{}, nodes)
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}

	p1 := mustSchedule(t, s, "g", "r1")
	if p1.Zone != "a" {
		t.Fatalf("first placement zone = %s, want a (all counts zero, zone id tie)", p1.Zone)
	}
	// a 已满；r2/r3 只能去 b、c（最小计数是已满的 a）。
	p2 := mustSchedule(t, s, "g", "r2")
	p3 := mustSchedule(t, s, "g", "r3")
	if p2.Zone == p3.Zone || (p2.Zone != "b" && p2.Zone != "c") {
		t.Fatalf("r2,r3 zones = %s,%s, want b,c", p2.Zone, p3.Zone)
	}
	// 计数 1/1/1，r4 让 b 或 c 变成 2，差 1，合法。
	p4 := mustSchedule(t, s, "g", "r4")
	if p4.Zone != "b" && p4.Zone != "c" {
		t.Fatalf("r4 zone = %s, want b or c", p4.Zone)
	}
	// r5：最小计数区为满区 a 与仍有空槽的 c，放入 c（差仍为 1）。
	p5 := mustSchedule(t, s, "g", "r5")
	if p5.Zone != "c" {
		t.Fatalf("r5 zone = %s, want c", p5.Zone)
	}
	// r6：计数 a=1(满),b=2,c=2，最小只剩已满的 a，其他区放置后差为 2 > S，拒绝。
	_, err := s.Schedule("g", "r6")
	if ErrReason(err) != ReasonSkewViolated {
		t.Fatalf("want skew_violated (full min zone blocks others), got %v", err)
	}
	counts, _ := s.ZoneCounts("g")
	if counts["a"] != 1 || counts["b"] != 2 || counts["c"] != 2 {
		t.Fatalf("counts after rejection = %v, want a=1,b=2,c=2", counts)
	}
}

func TestNonMatchingZoneExcludedFromMin(t *testing.T) {
	nodes := []Node{
		{ID: "n-other", Zone: "z-other", Slots: 4, Labels: map[string]string{"disk": "hdd"}},
		{ID: "n-a", Zone: "a", Slots: 1, Labels: map[string]string{"disk": "ssd"}},
		{ID: "n-b", Zone: "b", Slots: 4, Labels: map[string]string{"disk": "ssd"}},
	}
	s := newTestScheduler(t, Config{}, nodes)
	if err := s.AddGroup(Group{
		ID:             "g",
		Skew:           1,
		RequiredLabels: map[string]string{"disk": "ssd"},
	}); err != nil {
		t.Fatal(err)
	}
	p1 := mustSchedule(t, s, "g", "r1")
	if p1.Zone != "a" {
		t.Fatalf("first zone = %s, want a", p1.Zone)
	}
	mustSchedule(t, s, "g", "r2")
	mustSchedule(t, s, "g", "r3")
	// r3 后 b=2、a=1 且 a 已满；下一次只能选计数最小的 a（已满）。
	// z-other 不参与合格区最小值，故 r4 必须被拒绝。
	if _, err := s.Schedule("g", "r4"); ErrReason(err) != ReasonSkewViolated {
		t.Fatalf("want skew_violated excluding non-matching zone, got %v", err)
	}
	counts, _ := s.ZoneCounts("g")
	if _, present := counts["z-other"]; present {
		t.Fatalf("non-matching zone must not appear in counts: %v", counts)
	}
	if counts["a"] != 1 || counts["b"] != 2 {
		t.Fatalf("counts = %v, want a=1,b=2", counts)
	}
}

func TestNodeSelectionMostFreeThenIDSorted(t *testing.T) {
	nodes := []Node{
		{ID: "n-z1", Zone: "z", Slots: 2},
		{ID: "n-z3", Zone: "z", Slots: 4},
		{ID: "n-z2", Zone: "z", Slots: 4},
	}
	s := newTestScheduler(t, Config{}, nodes)
	if err := s.AddGroup(Group{ID: "g", Skew: 5}); err != nil {
		t.Fatal(err)
	}
	p := mustSchedule(t, s, "g", "r1")
	if p.NodeID != "n-z2" {
		t.Fatalf("node = %s, want n-z2 (most free, id tie-break)", p.NodeID)
	}
}

func TestNoNodeAvailableWhenAllFull(t *testing.T) {
	s := newTestScheduler(t, Config{}, []Node{{ID: "n-a", Zone: "a", Slots: 1}})
	if err := s.AddGroup(Group{ID: "g", Skew: 10}); err != nil {
		t.Fatal(err)
	}
	mustSchedule(t, s, "g", "r1")
	_, err := s.Schedule("g", "r2")
	if ErrReason(err) != ReasonNoNodeAvailable {
		t.Fatalf("want no_node_available, got %v", err)
	}
}
