package bikefence

import (
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return Config{
		Timezone:           loc,
		EvacNumerator:      4,
		EvacDenominator:    5,
		ClaimTimeout:       10 * time.Minute,
		DefaultOperatingID: "OP1",
		OutsideFee:         500,
		RewardAmount:       200,
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// registerLayout 注册一组典型围栏：
// OP1 大矩形 (0,0)-(100,100)，内含 NP1 禁停区、RW1 奖励区；OP2 另一个运营区。
// RW1 右边 x=100 与 OP1 边界重合，用于“紧贴”场景。
func registerLayout(t *testing.T, s *Service) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	op1 := []Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}}
	op2 := []Point{{200, 0}, {300, 0}, {300, 100}, {200, 100}}
	np1 := []Point{{10, 10}, {20, 10}, {20, 20}, {10, 20}}
	rw1 := []Point{{80, 10}, {100, 10}, {100, 30}, {80, 30}}
	must(s.RegisterFence("OP1", KindOperating, op1, 10, 1))
	must(s.RegisterFence("OP2", KindOperating, op2, 10, 2))
	must(s.RegisterFence("NP1", KindNoParking, np1, 5, 3))
	must(s.RegisterFence("RW1", KindReward, rw1, 2, 4))
}

func errKind(err error) ErrorKind {
	return err.(*Error).Kind
}

func bikeName(i int) string { return "b" + itoa(i) }

// 1) 点恰在围栏边界与顶点上：边界点视为在围栏内。
func TestBoundaryAndVertex(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	cases := []struct {
		name string
		p    Point
		want string
	}{
		{"op1-edge", Point{50, 0}, "OP1"},
		{"op1-vertex", Point{0, 0}, "OP1"},
		{"np-edge", Point{15, 10}, "NP1"},
		{"np-vertex", Point{10, 10}, "NP1"},
		{"reward-on-shared-edge", Point{100, 20}, "RW1"},
		{"reward-vertex-on-shared-edge", Point{100, 10}, "RW1"},
		{"outside", Point{150, 50}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := s.Locate(tc.p, 10)
			if err != nil {
				t.Fatalf("Locate: %v", err)
			}
			if res.FenceID != tc.want {
				t.Fatalf("p=%v got %q want %q (%s)", tc.p, res.FenceID, tc.want, res.Reason)
			}
		})
	}
}

// 2) 奖励区紧贴运营区边界：登记被接受，共享边上的点归奖励区并发放奖励。
func TestRewardHugsOperatingBoundary(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	if err := s.RegisterBike("b1", 5); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReturnBike("b1", "u1", Point{100, 20}, 6)
	if err != nil {
		t.Fatalf("return on shared edge: %v", err)
	}
	if r.FenceID != "RW1" || !r.RewardGranted {
		t.Fatalf("got %+v, want reward at RW1", r)
	}
}

// 3) 围栏恰好满员时的并发还车，只成功一次（容量 1）。
func TestConcurrentReturnOnlyOneSucceeds(t *testing.T) {
	s, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	op := []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	if err := s.RegisterFence("OP1", KindOperating, op, 1, 1); err != nil {
		t.Fatal(err)
	}
	const n = 16
	for i := 0; i < n; i++ {
		if err := s.RegisterBike(bikeName(i), int64(2+i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = s.ReturnBike(bikeName(i), "u", Point{5, 5}, 100)
		}(i)
	}
	close(start)
	wg.Wait()

	success, full := 0, 0
	for _, e := range results {
		switch {
		case e == nil:
			success++
		case errKind(e) == ErrFenceFull:
			full++
		default:
			t.Fatalf("unexpected error: %v", e)
		}
	}
	if success != 1 || full != n-1 {
		t.Fatalf("success=%d full=%d, want 1 and %d", success, full, n-1)
	}
	cnt, _ := s.FenceCount("OP1", 100)
	if cnt != 1 {
		t.Fatalf("count=%d want 1 (invariant: never exceed capacity)", cnt)
	}
}

// 4) 同一用户同一自然日两次奖励区还车只奖励一次；跨自然日再次奖励。
func TestRewardDailyLimit(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	day1 := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC).UnixMilli()
	if err := s.RegisterBike("b1", day1-10); err != nil {
		t.Fatal(err)
	}
	r1, err := s.ReturnBike("b1", "u1", Point{85, 20}, day1)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.RewardGranted || r1.Reward != 200 {
		t.Fatalf("first return: %+v", r1)
	}
	if err := s.Unlock("b1", day1+1000); err != nil {
		t.Fatal(err)
	}
	r2, err := s.ReturnBike("b1", "u1", Point{85, 20}, day1+2000)
	if err != nil {
		t.Fatal(err)
	}
	if r2.RewardGranted {
		t.Fatalf("same-day second return must not reward: %+v", r2)
	}
	day2 := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC).UnixMilli()
	if err := s.Unlock("b1", day2-1000); err != nil {
		t.Fatal(err)
	}
	r3, err := s.ReturnBike("b1", "u1", Point{85, 20}, day2)
	if err != nil {
		t.Fatal(err)
	}
	if !r3.RewardGranted {
		t.Fatalf("next-day return must reward: %+v", r3)
	}
	if err := s.RegisterBike("b2", day2+1); err != nil {
		t.Fatal(err)
	}
	r4, err := s.ReturnBike("b2", "u2", Point{85, 20}, day2+2)
	if err != nil || !r4.RewardGranted {
		t.Fatalf("different user must independently reward: r=%+v err=%v", r4, err)
	}
}

// 5) 比例恰等于阈值触发疏散（阈值 4/5，容量 10 => 第 8 辆取等触发）。
func TestEvacuateAtExactThreshold(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	for i := 0; i < 8; i++ {
		if err := s.RegisterBike(bikeName(i), int64(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ {
		if _, err := s.ReturnBike(bikeName(i), "u", Point{50, 50}, int64(20+i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.SnapshotState().Tasks) != 0 {
		t.Fatal("at count=7 (<8) no task expected")
	}
	if _, err := s.ReturnBike("b7", "u", Point{50, 50}, 30); err != nil {
		t.Fatal(err)
	}
	snap := s.SnapshotState()
	if len(snap.Tasks) != 1 {
		t.Fatalf("at count=8 == threshold want 1 evac task, got %d", len(snap.Tasks))
	}
	for _, task := range snap.Tasks {
		if task.Kind != TaskEvacuate || task.FromFenceID != "OP1" || task.ToFenceID != "RW1" {
			t.Fatalf("unexpected task %+v", task)
		}
	}
}

// 6) 已有未完成疏散任务时不再重复生成；完成后可再次生成。
func TestNoDuplicateEvacuation(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	// RW2 容量 5、阈值 4：可在“已有未完成疏散任务”时继续还第 5 辆，验证不重复生成。
	rw2 := []Point{{80, 60}, {100, 60}, {100, 90}, {80, 90}}
	if err := s.RegisterFence("RW2", KindReward, rw2, 5, 5); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := s.RegisterBike(bikeName(i), int64(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	put := func(bike string, at int64) {
		t.Helper()
		if _, err := s.ReturnBike(bike, "u", Point{85, 70}, at); err != nil {
			t.Fatalf("return %s: %v", bike, err)
		}
	}
	for i := 0; i < 3; i++ {
		put(bikeName(i), int64(20+i))
	}
	if len(s.SnapshotState().Tasks) != 0 {
		t.Fatalf("below threshold: no task, got %d", len(s.SnapshotState().Tasks))
	}
	put("b3", 23) // 第 4 辆：取等阈值，生成疏散任务
	snap := s.SnapshotState()
	if len(snap.Tasks) != 1 {
		t.Fatalf("threshold reached: want 1 task, got %d", len(snap.Tasks))
	}
	tid := ""
	for id := range snap.Tasks {
		tid = id
	}
	put("b4", 24) // 任务仍未完成：不得重复生成
	if len(s.SnapshotState().Tasks) != 1 {
		t.Fatalf("active task exists: must not duplicate, got %d", len(s.SnapshotState().Tasks))
	}
	if err := s.ClaimTask(tid, "w1", 25); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteTask(tid, "w1", Point{50, 50}, 26); err != nil {
		t.Fatalf("complete: %v", err)
	}
	put("b5", 27) // 完成后再次达到阈值：应生成新任务
	if len(s.SnapshotState().Tasks) != 2 {
		t.Fatalf("after completion a new evac task must be generated, got %d", len(s.SnapshotState().Tasks))
	}
}

// 7) 认领超时恰在时限那一刻释放，可被他人认领，旧认领记录保留。
func TestClaimTimeoutExactlyAtLimit(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	if err := s.RegisterBike("b1", 5); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReturnBike("b1", "u", Point{150, 50}, 6)
	if err != nil || r.TaskID == "" {
		t.Fatalf("outside return: r=%+v err=%v", r, err)
	}
	tid := r.TaskID
	claimed := int64(1000)
	if err := s.ClaimTask(tid, "w1", claimed); err != nil {
		t.Fatal(err)
	}
	limit := int64(10 * 60 * 1000)
	if err := s.ClaimTask(tid, "w2", claimed+limit-1); err == nil ||
		errKind(err) != ErrTaskClaimed {
		t.Fatalf("one ms before timeout must stay claimed, got %v", err)
	}
	if task, _ := s.GetTask(tid); task.Status != TaskInProgress {
		t.Fatalf("before timeout status=%v", task.Status)
	}
	if err := s.ClaimTask(tid, "w2", claimed+limit); err != nil {
		t.Fatalf("exactly at timeout should be reclaimable: %v", err)
	}
	task, _ := s.GetTask(tid)
	if task.Status != TaskInProgress || task.ClaimedBy != "w2" {
		t.Fatalf("after reclaim: %+v", task)
	}
	if len(task.Claims) != 2 || task.Claims[0].WorkerID != "w1" {
		t.Fatalf("old claim record must be kept, got %+v", task.Claims)
	}
}

// 8) 完成落点为满员围栏被拒后任务仍处理中、车辆不动；禁停区落点同样被拒。
func TestCompleteIntoFullFenceRejected(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	for i := 0; i < 10; i++ {
		id := "f" + itoa(i)
		if err := s.RegisterBike(id, int64(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		id := "f" + itoa(i)
		if _, err := s.ReturnBike(id, "u", Point{250, 50}, int64(20+i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RegisterBike("b1", 200); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReturnBike("b1", "u", Point{150, 50}, 201)
	if err != nil {
		t.Fatal(err)
	}
	tid := r.TaskID
	if err := s.ClaimTask(tid, "w1", 202); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteTask(tid, "w1", Point{250, 50}, 203); err == nil ||
		errKind(err) != ErrFenceFull {
		t.Fatalf("dropoff into full fence must be rejected, got %v", err)
	}
	if task, _ := s.GetTask(tid); task.Status != TaskInProgress || task.ClaimedBy != "w1" {
		t.Fatalf("task must stay in-progress: %+v", task)
	}
	if err := s.CompleteTask(tid, "w1", Point{15, 15}, 204); err == nil ||
		errKind(err) != ErrIllegalDropoff {
		t.Fatalf("dropoff into no-parking must be rejected, got %v", err)
	}
	if task, _ := s.GetTask(tid); task.Status != TaskInProgress {
		t.Fatalf("task must still be in-progress: %+v", task)
	}
	if err := s.CompleteTask(tid, "w1", Point{50, 50}, 205); err != nil {
		t.Fatalf("completion into OP1: %v", err)
	}
	if task, _ := s.GetTask(tid); task.Status != TaskDone {
		t.Fatalf("task done expected, got %+v", task)
	}
}

// 围栏约束：相交、未完全落入运营区、顶点重合、容量非正均拒绝；拒绝不推进时钟。
func TestRegisterConstraints(t *testing.T) {
	s := newTestService(t)
	op1 := []Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}}
	if err := s.RegisterFence("OP1", KindOperating, op1, 10, 10); err != nil {
		t.Fatal(err)
	}
	check := func(name string, err error, want ErrorKind) {
		t.Helper()
		if err == nil || errKind(err) != want {
			t.Fatalf("%s: got %v want kind %d", name, err, want)
		}
	}
	op2 := []Point{{50, 50}, {150, 50}, {150, 150}, {50, 150}}
	check("intersecting-operating", s.RegisterFence("OP2", KindOperating, op2, 5, 11), ErrFenceConstraint)
	out := []Point{{110, 110}, {120, 110}, {120, 120}, {110, 120}}
	check("reward-not-inside", s.RegisterFence("RW9", KindReward, out, 5, 11), ErrFenceConstraint)
	bad := []Point{{0, 0}, {10, 0}, {10, 0}, {0, 10}}
	check("duplicate-adjacent-vertex", s.RegisterFence("OP9", KindOperating, bad, 5, 11), ErrInvalidArgument)
	check("bad-capacity", s.RegisterFence("OP8", KindOperating,
		[]Point{{200, 200}, {210, 200}, {210, 210}, {200, 210}}, 0, 11), ErrInvalidArgument)
	if err := s.RegisterFence("OP3", KindOperating,
		[]Point{{200, 200}, {210, 200}, {210, 210}, {200, 210}}, 5, 11); err != nil {
		t.Fatalf("clock must not advance after rejection: %v", err)
	}
	if err := s.RegisterFence("OP4", KindOperating,
		[]Point{{300, 300}, {310, 300}, {310, 310}, {300, 310}}, 5, 5); err == nil ||
		errKind(err) != ErrClockBackwards {
		t.Fatalf("clock backwards expected, got %v", err)
	}
}

// 区外调度费与奖励互斥；被拒绝的禁停区还车不改变车辆状态。
func TestOutsideFeeAndNoParkingReject(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	if err := s.RegisterBike("b1", 5); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReturnBike("b1", "u", Point{150, 50}, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Outside || r.Fee != 500 || r.Reward != 0 || r.TaskID == "" {
		t.Fatalf("outside receipt wrong: %+v", r)
	}
	if err := s.Unlock("b1", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReturnBike("b1", "u", Point{15, 15}, 8); err == nil ||
		errKind(err) != ErrNoParkingReturn {
		t.Fatalf("no-parking reject expected, got %v", err)
	}
	r2, err := s.ReturnBike("b1", "u", Point{50, 50}, 9)
	if err != nil || !r2.Accepted || r2.Fee != 0 {
		t.Fatalf("bike must still be riding and returnable: r=%+v err=%v", r2, err)
	}
}

// 复杂度证明：2000 个互不相交围栏中查询单点，访问 R 树节点数远小于围栏数，
// 且树高为常量级别；NodesVisited 即被裁剪掉的分支的可验证证据。
func TestSpatialIndexSublinear(t *testing.T) {
	s, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for r := 0; r < 20; r++ {
		for c := 0; c < 100; c++ {
			x0 := int64(c * 20)
			y0 := int64(r * 20)
			vs := []Point{{x0, y0}, {x0 + 10, y0}, {x0 + 10, y0 + 10}, {x0, y0 + 10}}
			id := "f" + itoa(r*100+c)
			if err := s.RegisterFence(id, KindOperating, vs, 1, int64(1+r*100+c)); err != nil {
				t.Fatal(err)
			}
		}
	}
	res, err := s.Locate(Point{5, 5}, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if res.FenceID != "f0" {
		t.Fatalf("want f0, got %s (%s)", res.FenceID, res.Reason)
	}
	if res.NodesVisited >= 100 {
		t.Fatalf("nodes visited %d is not sublinear vs 2000 fences", res.NodesVisited)
	}
	if h := s.c.tree.height(); h != 3 {
		t.Fatalf("tree height=%d want 3", h)
	}
	t.Logf("2000 fences: query visited %d rtree nodes (naive would test 2000); tree height=3",
		res.NodesVisited)

	// FenceCount 为 O(1) 直接计数，不扫描车辆：连续读取值一致且无遍历动作。
	cnt, err := s.FenceCount("f0", 3001)
	if err != nil || cnt != 0 {
		t.Fatalf("FenceCount=%d err=%v", cnt, err)
	}
}

// 错误优先级：同时满足多类错误时只报次序最靠前的一类。
func TestErrorOrdering(t *testing.T) {
	s := newTestService(t)
	registerLayout(t, s)
	// 参数非法优先于时钟回退。
	if _, err := s.ReturnBike("", "u", Point{5, 5}, 1); err == nil ||
		errKind(err) != ErrInvalidArgument {
		t.Fatalf("invalid argument must take precedence, got %v", err)
	}
	// 时钟回退优先于车辆不存在。
	if _, err := s.ReturnBike("nope", "u", Point{5, 5}, 0); err == nil ||
		errKind(err) != ErrClockBackwards {
		t.Fatalf("clock backwards must take precedence, got %v", err)
	}
	// 车辆不存在优先于“不在骑行中”。
	if _, err := s.ReturnBike("ghost", "u", Point{5, 5}, 100); err == nil ||
		errKind(err) != ErrBikeNotFound {
		t.Fatalf("bike not found expected, got %v", err)
	}
	if err := s.RegisterBike("b1", 101); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReturnBike("b1", "u", Point{5, 5}, 102); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReturnBike("b1", "u", Point{5, 5}, 103); err == nil ||
		errKind(err) != ErrBikeNotRiding {
		t.Fatalf("not riding expected, got %v", err)
	}
	// 禁停区优先于围栏已满（NP1 不会满，逻辑上禁停判定先于容量）。
	if err := s.RegisterBike("b2", 104); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReturnBike("b2", "u", Point{15, 15}, 105); err == nil ||
		errKind(err) != ErrNoParkingReturn {
		t.Fatalf("no-parking expected, got %v", err)
	}
}
