package dbscan

import (
	"testing"
)

func TestDistanceBoundary(t *testing.T) {
	s, _ := New(5, 4, 100, 100)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 3, 4)    // 距离恰为 5：互为邻居
	r, err := s.Insert(3, 4, -3) // 距点1 恰 5（含，16+9），距点2 为 sqrt(50)>5（不含）
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 1 || r.Changes[0].NewLabel != 0 {
		t.Fatalf("new point should remain noise: %v", r.Changes)
	}
	nb, _ := s.Neighbors(1)
	if !reflectInt64(nb, []int64{1, 2, 3}) {
		t.Fatalf("N(1)=%v want [1 2 3]", nb)
	}
	nb, _ = s.Neighbors(2)
	if !reflectInt64(nb, []int64{1, 2}) {
		t.Fatalf("N(2)=%v want [1 2] (distance 10 > eps)", nb)
	}
}

func TestCoreThresholdAndMinPtsOne(t *testing.T) {
	// minPts=3：恰好 3 邻居是核心，少 1 不是。
	s, _ := New(5, 3, 100, 100)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 1, 0)
	r := mustInsert(t, s, 3, 2, 0) // 点1、2 各有 3 个邻居，标签取最小核心 1
	wantChanges(t, r, []Change{{1, 0, 1}, {2, 0, 1}, {3, -1, 1}})
	r = mustInsert(t, s, 4, 10, 0) // 远离
	wantChanges(t, r, []Change{{4, -1, 0}})
	if lab, _ := s.Label(4); lab != 0 {
		t.Fatalf("isolated point minPts=3 must be noise, got %d", lab)
	}

	// minPts=1：每个点都是核心。
	s, _ = New(5, 1, 100, 100)
	r = mustInsert(t, s, 7, 0, 0)
	wantChanges(t, r, []Change{{7, -1, 7}})
	r = mustInsert(t, s, 8, 100, 100) // 不相邻，自成一簇
	wantChanges(t, r, []Change{{8, -1, 8}})
	r = mustInsert(t, s, 9, 1, 0) // 与 7 相邻，合并到较小标签 7
	wantChanges(t, r, []Change{{9, -1, 7}})
}

func TestMergeTakesSmallerLabel(t *testing.T) {
	// 两个已存在的簇，插入点成为核心并接通它们，标签取最小。
	s, _ := New(3, 2, 100, 100)
	// 簇 A：三点 (0,0),(3,0),(6,0)，互为邻居（dist<=3），均为核心，标签 2。
	mustInsert(t, s, 2, 0, 0)
	mustInsert(t, s, 3, 3, 0)
	mustInsert(t, s, 4, 6, 0)
	// 簇 B：标签 8
	mustInsert(t, s, 8, 18, 0)
	mustInsert(t, s, 9, 21, 0)
	mustInsert(t, s, 10, 24, 0)
	// 间隙 (9,0),(12,0),(15,0)。插入后 5@9 先扩 A，6@12 尚不能接通（与 8 距离6），
	// 7@15 插入后同时连接 6 与 8，产生 Merge，最终标签取最小 2。
	mustInsert(t, s, 5, 9, 0)
	mustInsert(t, s, 6, 12, 0)
	r := mustInsert(t, s, 7, 15, 0)
	if ev := findEvent(r, EventMerge); ev == nil {
		t.Fatalf("want Merge event bridging, got %v", r.Events)
	}
	for _, id := range []int64{2, 3, 4, 5, 6, 7, 8, 9, 10} {
		if lab, _ := s.Label(id); lab != 2 {
			t.Fatalf("point %d label=%d want 2", id, lab)
		}
	}
}

func TestBoundaryTieSmallerLabel(t *testing.T) {
	// 边界点同时与标签 4 与标签 8 的核心相邻：取 4（不是先到者/最近者）。
	s, _ := New(5, 2, 100, 100)
	mustInsert(t, s, 4, 0, 0)  // 核心（与点5 相距3）
	mustInsert(t, s, 5, 3, 0)  // 核心
	mustInsert(t, s, 8, 10, 0) // 另一个簇（与点9 相距3），标签 8
	mustInsert(t, s, 9, 13, 0)
	// 点 20 在 (5,0)：距点4(0,0) 与点8(10,0) 都恰为 5（含），标签取较小簇 4
	r := mustInsert(t, s, 20, 5, 0)
	wantOne := false
	for _, c := range r.Changes {
		if c.ID == 20 && c.NewLabel == 4 {
			wantOne = true
		}
	}
	if !wantOne {
		t.Fatalf("boundary point must take min core-cluster label 4: %v", r.Changes)
	}
}

func TestBoundaryBecomesNoise(t *testing.T) {
	s, _ := New(3, 3, 100, 100)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 1, 0)
	mustInsert(t, s, 3, 2, 0) // 点1、2 为核心
	mustInsert(t, s, 4, 3, 0) // 边界，标签 1
	if lab, _ := s.Label(4); lab != 1 {
		t.Fatalf("label 4=%d want 1", lab)
	}
	mustInsert(t, s, 9, 100, 0) // 远离，无关点
	// 删除点 2 后点 1 仍是核心；再删点 1，没有任何核心与 4 相邻。
	if _, err := s.Remove(2); err != nil {
		t.Fatal(err)
	}
	r, err := s.Remove(1)
	if err != nil {
		t.Fatal(err)
	}
	if lab, _ := s.Label(4); lab != 0 {
		t.Fatalf("after all adjacent cores removed, 4 must be noise, got %d (changes=%v)", lab, r.Changes)
	}
}

func TestExpiryExactBoundary(t *testing.T) {
	s, _ := New(5, 1, 10, 100)
	mustInsert(t, s, 1, 0, 0)
	r, err := s.Tick(9) // born 0 + W 10 = 10 > 9：存活
	if err != nil || len(r.Changes) != 0 {
		t.Fatalf("at t=9 point must remain alive: %v %v", r, err)
	}
	if _, ok := s.Label(1); !ok {
		t.Fatal("point expired one tick early")
	}
	r, err = s.Tick(10) // 恰好 born+W<=now：过期
	if err != nil {
		t.Fatal(err)
	}
	wantChanges(t, r, []Change{{1, 1, -1}})
	if s.Alive() != 0 {
		t.Fatalf("alive=%d", s.Alive())
	}
}

func TestBatchTickOnlyReportsDifferences(t *testing.T) {
	s, _ := New(2, 3, 10, 100)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 2, 0)
	mustInsert(t, s, 3, 4, 0) // 簇标签 2
	r, err := s.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	// 三点同时过期，点 1 是边界点：前后都不是自身标签保留问题，只报 -1。
	wantChanges(t, r, []Change{{1, 2, -1}, {2, 2, -1}, {3, 2, -1}})
	if len(r.Events) != 1 || r.Events[0].Type != EventDeath {
		t.Fatalf("events=%v", r.Events)
	}
}

func TestRemoveThenReinsertSameID(t *testing.T) {
	s, _ := New(5, 1, 100, 100)
	mustInsert(t, s, 1, 0, 0)
	if _, err := s.Remove(1); err != nil {
		t.Fatal(err)
	}
	r := mustInsert(t, s, 1, 9, 9) // 同编号复用
	wantChanges(t, r, []Change{{1, -1, 1}})
}

func TestCapacityRejectsAndRecovers(t *testing.T) {
	s, _ := New(5, 1, 100, 2)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 1, 0)
	_, err := s.Insert(3, 2, 0)
	if err == nil || err.(*OpError).Reason != ReasonCapacityFull {
		t.Fatalf("want capacity full, got %v", err)
	}
	if s.Alive() != 2 {
		t.Fatal("rejected insert changed state")
	}
	if _, err := s.Remove(1); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, s, 3, 2, 0)
}

func TestRejectedOpsNoStateChange(t *testing.T) {
	s, _ := New(5, 2, 10, 10)
	mustInsert(t, s, 1, 0, 0)
	// 参数非法优先于已存在
	_, err := s.Insert(-1, 0, 0)
	expectReason(t, err, ReasonInvalidParam)
	_, err = s.Insert(1, 0, 0)
	expectReason(t, err, ReasonIDExists)
	_, err = s.Insert(2, 1_000_001, 0)
	expectReason(t, err, ReasonInvalidParam)
	// 填满容量，已满优先于... 参数非法仍最先
	for i := int64(2); i <= 10; i++ {
		mustInsert(t, s, i, int64(i)*100, 0)
	}
	_, err = s.Insert(11, 0, 0)
	expectReason(t, err, ReasonCapacityFull)
	_, err = s.Insert(-11, 0, 0)
	expectReason(t, err, ReasonInvalidParam)
	// Remove 参数非法 / 不存在
	_, err = s.Remove(0)
	expectReason(t, err, ReasonInvalidParam)
	_, err = s.Remove(99)
	expectReason(t, err, ReasonIDNotFound)
	// Tick 参数非法优先于时钟回退
	_, err = s.Tick(1_000_000_000_000_001)
	expectReason(t, err, ReasonInvalidParam)
	_, err = s.Tick(-1)
	expectReason(t, err, ReasonInvalidParam)
	if _, err := s.Tick(5); err != nil {
		t.Fatal(err)
	}
	_, err = s.Tick(4)
	expectReason(t, err, ReasonClockRewind)
	// t==now 允许
	r, err := s.Tick(5)
	if err != nil || len(r.Changes) != 0 {
		t.Fatalf("equal-time tick must be a no-op: %v %v", r, err)
	}
	if s.Now() != 5 {
		t.Fatalf("now=%d", s.Now())
	}
}

func TestInvalidConstructor(t *testing.T) {
	cases := [][4]int64{
		{0, 2, 10, 10}, {1_000_001, 2, 10, 10},
		{5, 0, 10, 10}, {5, 1001, 10, 10},
		{5, 2, 0, 10}, {5, 2, 1_000_000_001, 10},
		{5, 2, 10, 0}, {5, 2, 10, 100001},
	}
	for _, c := range cases {
		if _, err := New(c[0], int(c[1]), c[2], int(c[3])); err == nil {
			t.Fatalf("params %v must be rejected", c)
		}
	}
}
