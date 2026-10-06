package transformer

import (
	"math/rand"
	"sync"
	"testing"
)

func mustCreate(t *testing.T, s *System, feeder string, start, end, power int) int {
	t.Helper()
	id, err := s.Create(feeder, start, end, power)
	if err != nil {
		t.Fatalf("Create(%s,%d,%d,%d) 意外失败: %v", feeder, start, end, power, err)
	}
	return id
}

func mustConfirm(t *testing.T, s *System, id int) {
	t.Helper()
	if err := s.Confirm(id); err != nil {
		t.Fatalf("Confirm(%d) 意外失败: %v", id, err)
	}
}

func wantErr(t *testing.T, got *Error, cat ErrCategory) *Error {
	t.Helper()
	if got == nil {
		t.Fatalf("期望错误 %s，实际成功", cat)
	}
	if got.Category != cat {
		t.Fatalf("期望错误 %s，实际 %v", cat, got)
	}
	return got
}

func wantStatus(t *testing.T, s *System, id int, st Status) {
	t.Helper()
	r, ok := s.Reservation(id)
	if !ok {
		t.Fatalf("预约 %d 不存在", id)
	}
	if r.Status != st {
		t.Fatalf("预约 %d 状态期望 %s，实际 %s", id, st, r.Status)
	}
}

// 区间端点相接不冲突（左闭右开）。
func TestAdjacentIntervalsNoConflict(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 5}, map[string]int{"f": 5}, 5)
	a := mustCreate(t, s, "f", 0, 10, 5)
	b := mustCreate(t, s, "f", 10, 20, 5)
	mustConfirm(t, s, a)
	mustConfirm(t, s, b)
}

// 占位恰在到期时刻确认被拒，且预约转为已失效；到期前一刻确认成功。
func TestConfirmAtHoldExpiry(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 5}, map[string]int{"f": 10}, 10)
	a := mustCreate(t, s, "f", 0, 10, 1) // 到期时刻 = 0+5 = 5
	if err := s.AdvanceTime(4); err != nil {
		t.Fatal(err)
	}
	mustConfirm(t, s, a)                 // 到期前确认成功
	c := mustCreate(t, s, "f", 4, 10, 1) // 创建时刻 4，到期时刻 = 4+5 = 9
	if err := s.AdvanceTime(9); err != nil {
		t.Fatal(err)
	}
	err := s.Confirm(c)
	wantErr(t, err, ErrHoldExpired) // 恰在到期时刻确认被拒
	wantStatus(t, s, c, StatusExpired)
}

// 馈线与变压器同时不足时报馈线，并给出首个不满足时刻。
func TestFeederReportedWhenBothInsufficient(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 5}, map[string]int{"f": 3}, 3)
	a := mustCreate(t, s, "f", 0, 10, 3)
	mustConfirm(t, s, a)
	_, err := s.Create("f", 0, 10, 1) // 馈线 3+1>3 且变压器 3+1>3
	e := wantErr(t, err, ErrCapacity)
	if e.Level != LevelFeeder || e.Time != 0 {
		t.Fatalf("期望馈线层级时刻 0，实际层级 %s 时刻 %d", e.Level, e.Time)
	}
}

// 变压器层级容量不足时报告首个不满足的时刻。
func TestTransformerLevelFirstFailTime(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 5}, map[string]int{"f0": 10, "f1": 10}, 5)
	a := mustCreate(t, s, "f0", 3, 8, 5)
	mustConfirm(t, s, a)
	_, err := s.Create("f1", 0, 10, 1) // t∈[3,8) 变压器 5+1>5
	e := wantErr(t, err, ErrCapacity)
	if e.Level != LevelTransformer || e.Time != 3 {
		t.Fatalf("期望变压器层级时刻 3，实际层级 %s 时刻 %d", e.Level, e.Time)
	}
}

// 改约校验排除自身原占用；失败时原预约原样保留。
func TestModifyExcludesSelf(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 50}, map[string]int{"f": 10}, 10)
	id := mustCreate(t, s, "f", 0, 10, 10)
	// 若计入自身，[5,10) 处馈线占用 10+10>10 必失败。
	if err := s.Modify(id, 5, 15, 10); err != nil {
		t.Fatalf("改约排除自身应成功: %v", err)
	}
	r, _ := s.Reservation(id)
	if r.Start != 5 || r.End != 15 || r.Status != StatusHolding || r.HoldExpiresAt != 50 {
		t.Fatalf("改约后字段不符: %+v", r)
	}
	mustConfirm(t, s, id)
	if err := s.Modify(id, 8, 20, 10); err != nil {
		t.Fatalf("已确认改约排除自身应成功: %v", err)
	}

	// 失败时原样保留：另一系统里其他预约占满 [12,20)，改约必撞。
	s2 := NewSystem(Config{HoldDuration: 50}, map[string]int{"f": 10}, 10)
	x := mustCreate(t, s2, "f", 0, 10, 10)
	y := mustCreate(t, s2, "f", 12, 20, 10)
	mustConfirm(t, s2, y)
	err := s2.Modify(x, 5, 15, 10) // [12,15) 处馈线 10+10>10
	e := wantErr(t, err, ErrCapacity)
	if e.Level != LevelFeeder || e.Time != 12 {
		t.Fatalf("期望馈线层级时刻 12，实际层级 %s 时刻 %d", e.Level, e.Time)
	}
	r2, _ := s2.Reservation(x)
	if r2.Start != 0 || r2.End != 10 || r2.Power != 10 || r2.Status != StatusHolding {
		t.Fatalf("改约失败后原预约应保持不变: %+v", r2)
	}
}

// 容量下调被已确认预约挡住：整体拒绝并报首个超出时刻，容量表不变。
func TestCapacityDecreaseBlockedByConfirmed(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 100}, map[string]int{"f0": 10, "f1": 10}, 12)
	a := mustCreate(t, s, "f0", 0, 10, 6)
	b := mustCreate(t, s, "f1", 5, 15, 5)
	mustConfirm(t, s, a)
	mustConfirm(t, s, b) // t∈[5,10) 已确认之和 = 11
	err := s.AddCapacityRecord(5, 10)
	e := wantErr(t, err, ErrCapacity)
	if e.Level != LevelTransformer || e.Time != 5 {
		t.Fatalf("期望变压器层级时刻 5，实际层级 %s 时刻 %d", e.Level, e.Time)
	}
	if got := s.CapacityAt(5); got != 12 {
		t.Fatalf("变更被拒绝后容量表不应改变，CapacityAt(5)=%d", got)
	}
}

// 容量下调被接受后，按创建时刻从晚到早取消占位中的预约。
func TestCapacityDecreaseCancelsHoldsLatestFirst(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 1000}, map[string]int{"f": 20}, 16)
	c := mustCreate(t, s, "f", 0, 20, 4)
	mustConfirm(t, s, c)
	h1 := mustCreate(t, s, "f", 0, 20, 4) // 创建时刻 0
	if err := s.AdvanceTime(1); err != nil {
		t.Fatal(err)
	}
	h2 := mustCreate(t, s, "f", 1, 20, 4) // 创建时刻 1
	if err := s.AdvanceTime(2); err != nil {
		t.Fatal(err)
	}
	h3 := mustCreate(t, s, "f", 2, 20, 4) // 创建时刻 2，变压器总和 16
	// 下调到 9：已确认 4 <= 9 接受；t∈[2,20) 总和 16>9，
	// 先取消 h3（12>9），再取消 h2（8<=9 停），h1 保留。
	if err := s.AddCapacityRecord(2, 9); err != nil {
		t.Fatalf("容量变更应被接受: %v", err)
	}
	wantStatus(t, s, h3, StatusCancelled)
	wantStatus(t, s, h2, StatusCancelled)
	wantStatus(t, s, h1, StatusHolding)
	wantStatus(t, s, c, StatusConfirmed) // 已确认预约不会被容量变更取消
	wantErr(t, s.Confirm(h2), ErrCancelled)
	mustConfirm(t, s, h1)
}

// 容量记录生效时刻恰等于某预约起点时，从该时刻起生效。
func TestCapacityRecordEffectiveAtReservationStart(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 100}, map[string]int{"f": 10}, 10)
	a := mustCreate(t, s, "f", 10, 20, 6)
	mustConfirm(t, s, a)
	if err := s.AddCapacityRecord(10, 6); err != nil {
		t.Fatalf("恰等于预约起点的容量记录应被接受: %v", err)
	}
	if got := s.CapacityAt(9); got != 10 {
		t.Fatalf("时刻 9 容量应为 10，实际 %d", got)
	}
	if got := s.CapacityAt(10); got != 6 {
		t.Fatalf("时刻 10 容量应为 6，实际 %d", got)
	}
	_, err := s.Create("f", 10, 20, 1) // t=10 起变压器 6+1>6
	e := wantErr(t, err, ErrCapacity)
	if e.Level != LevelTransformer || e.Time != 10 {
		t.Fatalf("期望变压器层级时刻 10，实际层级 %s 时刻 %d", e.Level, e.Time)
	}
	// 下调到 5：t=10 起已确认 6>5，拒绝且首个超出时刻为 10。
	err = s.AddCapacityRecord(10, 5)
	e = wantErr(t, err, ErrCapacity)
	if e.Time != 10 {
		t.Fatalf("期望首个超出时刻 10，实际 %d", e.Time)
	}
}

// 已开始的已确认预约只能提前结束，结束时刻不得早于当前时刻。
func TestEarlyEndStartedConfirmed(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 5}, map[string]int{"f": 10}, 10)
	id := mustCreate(t, s, "f", 0, 100, 5)
	mustConfirm(t, s, id)
	if err := s.AdvanceTime(50); err != nil {
		t.Fatal(err)
	}
	if err := s.Modify(id, 0, 60, 5); err != nil {
		t.Fatalf("提前结束应成功: %v", err)
	}
	r, _ := s.Reservation(id)
	if r.End != 60 || r.Status != StatusConfirmed {
		t.Fatalf("提前结束后字段不符: %+v", r)
	}
	wantErr(t, s.Modify(id, 0, 40, 5), ErrInvalidParam)  // 终点早于当前
	wantErr(t, s.Modify(id, 0, 120, 5), ErrInvalidParam) // 延长不允许
	wantErr(t, s.Modify(id, 0, 60, 6), ErrInvalidParam)  // 改功率不允许
	wantErr(t, s.Modify(id, 10, 60, 5), ErrInvalidParam) // 改起点不允许
	// [60,100) 的占用已归还。
	mustCreate(t, s, "f", 70, 100, 10)
	if err := s.AdvanceTime(60); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, s, id, StatusCompleted)
}

// 拒绝次序：参数非法 > 时钟回退 > 预约不存在 > 状态不允许 > 占位已到期 > 已取消 > 容量不足。
func TestRejectionOrder(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 5}, map[string]int{"f": 10}, 8)
	// 参数非法优先于预约不存在。
	wantErr(t, s.Modify(999, 10, 5, 1), ErrInvalidParam)
	wantErr(t, s.Modify(999, 5, 10, 1), ErrNotFound)
	// 参数非法：馈线不存在、功率非正、功率超上限、区间非法。
	_, err := s.Create("ghost", 0, 10, 1)
	wantErr(t, err, ErrInvalidParam)
	_, err = s.Create("f", 0, 10, 0)
	wantErr(t, err, ErrInvalidParam)
	_, err = s.Create("f", 0, 10, 11)
	wantErr(t, err, ErrInvalidParam)
	_, err = s.Create("f", 5, 5, 1)
	wantErr(t, err, ErrInvalidParam)
	// 时钟回退，且当前时刻不变。
	if err := s.AdvanceTime(3); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.AdvanceTime(2), ErrClockRewind)
	if s.Now() != 3 {
		t.Fatalf("时钟回退被拒绝后当前时刻不应改变，Now()=%d", s.Now())
	}
	// 预约不存在。
	wantErr(t, s.Confirm(999), ErrNotFound)
	wantErr(t, s.Release(999), ErrNotFound)
	// 占位已到期：确认报占位已到期，改约/释放报状态不允许。
	h := mustCreate(t, s, "f", 3, 10, 1) // 到期时刻 3+5=8
	if err := s.AdvanceTime(8); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Confirm(h), ErrHoldExpired)
	wantErr(t, s.Modify(h, 8, 12, 1), ErrInvalidState)
	wantErr(t, s.Release(h), ErrInvalidState)
	// 已取消：容量下调挤占后确认报已取消。
	c := mustCreate(t, s, "f", 8, 20, 4)
	mustConfirm(t, s, c)
	hold := mustCreate(t, s, "f", 8, 20, 4) // 变压器总和 8
	if err := s.AddCapacityRecord(8, 4); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, s, hold, StatusCancelled)
	wantErr(t, s.Confirm(hold), ErrCancelled)
	// 状态不允许：重复确认、对已完成预约操作。
	wantErr(t, s.Confirm(c), ErrInvalidState)
	if err := s.AdvanceTime(20); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, s, c, StatusCompleted)
	wantErr(t, s.Release(c), ErrInvalidState)
}

// 释放立即归还占用；已完成的预约不再占用。
func TestReleaseAndCompletionFreeOccupancy(t *testing.T) {
	s := NewSystem(Config{HoldDuration: 5}, map[string]int{"f": 10}, 10)
	a := mustCreate(t, s, "f", 0, 10, 10)
	_, err := s.Create("f", 0, 10, 1)
	wantErr(t, err, ErrCapacity)
	if err := s.Release(a); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, s, a, StatusReleased)
	wantErr(t, s.Release(a), ErrInvalidState)
	b := mustCreate(t, s, "f", 0, 10, 10) // 占用已归还
	mustConfirm(t, s, b)
	if err := s.AdvanceTime(10); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, s, b, StatusCompleted)
	mustCreate(t, s, "f", 10, 20, 10) // 已完成不再占用
}

// 并发调用等价于某个串行顺序：不变量在任意交错下保持。
func TestConcurrentSerializable(t *testing.T) {
	limits := map[string]int{"f0": 4, "f1": 4}
	s := NewSystem(Config{HoldDuration: 3}, limits, 6)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				now := s.Now()
				switch rng.Intn(6) {
				case 0:
					f := "f0"
					if rng.Intn(2) == 0 {
						f = "f1"
					}
					start := now + rng.Intn(20)
					_, _ = s.Create(f, start, start+1+rng.Intn(8), 1+rng.Intn(5))
				case 1:
					_ = s.Confirm(1 + rng.Intn(64))
				case 2:
					_ = s.Release(1 + rng.Intn(64))
				case 3:
					start := now + rng.Intn(20)
					_ = s.Modify(1+rng.Intn(64), start, start+1+rng.Intn(8), 1+rng.Intn(5))
				case 4:
					_ = s.AddCapacityRecord(now+rng.Intn(20), 2+rng.Intn(8))
				default:
					_ = s.AdvanceTime(now + rng.Intn(4))
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	assertInvariants(t, s, limits)
}

// assertInvariants 从快照重算逐时刻占用，校验两级不变量（当前时刻起）。
func assertInvariants(t *testing.T, s *System, limits map[string]int) {
	t.Helper()
	now := s.Now()
	feederSum := map[string]map[int]int{}
	transSum := map[int]int{}
	for _, r := range s.Snapshot() {
		if r.Status != StatusHolding && r.Status != StatusConfirmed {
			continue
		}
		if feederSum[r.Feeder] == nil {
			feederSum[r.Feeder] = map[int]int{}
		}
		for tt := r.Start; tt < r.End; tt++ {
			if tt < now {
				continue
			}
			feederSum[r.Feeder][tt] += r.Power
			transSum[tt] += r.Power
		}
	}
	for f, slots := range feederSum {
		for tt, v := range slots {
			if v > limits[f] {
				t.Errorf("馈线 %s 时刻 %d 占用 %d 超上限 %d", f, tt, v, limits[f])
			}
		}
	}
	for tt, v := range transSum {
		if c := s.CapacityAt(tt); v > c {
			t.Errorf("变压器时刻 %d 占用 %d 超容量 %d", tt, v, c)
		}
	}
}
