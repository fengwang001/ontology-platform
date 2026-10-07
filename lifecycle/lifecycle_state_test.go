package lifecycle

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newHarness(t *testing.T) (*Service, *NaiveReference, *FakeClock, *SliceLogger, *CountingAuditLog) {
	t.Helper()
	clock := NewFakeClock(t0)
	log := NewSliceLogger()
	audit := NewCountingAuditLog(NewMemoryAuditLog())
	svc := NewService(clock, audit, log)
	naive := NewNaiveReference(clock)
	return svc, naive, clock, log, audit
}

func mustCreate(t *testing.T, svc *Service, naive *NaiveReference, id string) {
	t.Helper()
	if err := svc.CreateObject(id, Attrs{"name": id}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	if naive != nil {
		if err := naive.CreateObject(id, Attrs{"name": id}); err != nil {
			t.Fatalf("naive create %s: %v", id, err)
		}
	}
}

func errIs(err, target error) bool { return errors.Is(err, target) }

// 宽限截止时刻“恰好到达”与“刚过去”两种临界情形。
func TestGraceBoundaryExactAndPast(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)
	mustCreate(t, svc, nil, "o")

	// t0+10 进入宽限，截止时刻 t0+20。
	clock.Set(t0.Add(10 * time.Second))
	deadline := t0.Add(20 * time.Second)
	if err := svc.Delete("o", IdentityUser, deadline); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// 时刻之前一刻：撤销成功，回到存活，删除记录不再影响查询。
	clock.Set(deadline.Add(-1 * time.Nanosecond))
	if err := svc.Undo("o", IdentityAdmin); err != nil {
		t.Fatalf("undo before deadline: %v", err)
	}
	if got := svc.GetView("o", IdentityUser); got.State != StateAlive || !got.Visible {
		t.Fatalf("after undo: %+v", got)
	}

	// 再来一轮：删除后在“恰好到达”时刻发起撤销必须被拒绝
	// （方向不允许），且下一次操作自动归档。
	clock.Set(t0.Add(30 * time.Second))
	deadline2 := t0.Add(40 * time.Second)
	if err := svc.Delete("o", IdentityUser, deadline2); err != nil {
		t.Fatalf("delete2: %v", err)
	}
	clock.Set(deadline2) // 恰好到达
	err := svc.Undo("o", IdentityAdmin)
	if !errIs(err, ErrInvalidTransition) {
		t.Fatalf("undo exactly at deadline = %v, want ErrInvalidTransition", err)
	}
	if got := svc.GetView("o", IdentityAdmin); got.State != StateArchived {
		t.Fatalf("state after deadline reached = %s, want archived", got.State)
	}

	// 第三轮：刚过去（deadline+1ns）时任何操作触发自动归档，不额外消耗请求。
	mustCreate(t, svc, nil, "p")
	clock.Set(t0.Add(50 * time.Second))
	dp := t0.Add(60 * time.Second)
	if err := svc.Delete("p", IdentityUser, dp); err != nil {
		t.Fatalf("delete p: %v", err)
	}
	clock.Set(dp.Add(1 * time.Nanosecond))
	v := svc.GetView("p", IdentityAdmin) // 查询即“涉及该对象的操作”
	if v.State != StateArchived {
		t.Fatalf("state just past deadline = %s, want archived", v.State)
	}
	// 归档为终态：撤销被拒绝；归档幂等成功。
	if err := svc.Undo("p", IdentityAdmin); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("undo archived = %v", err)
	}
	if err := svc.Archive("p", IdentityAdmin); err != nil {
		t.Fatalf("archive archived = %v, want idempotent success", err)
	}
}

// 非法宽限截止与非法冻结时长。
func TestInvalidTimeValues(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)
	mustCreate(t, svc, nil, "o")

	if err := svc.Delete("o", IdentityAdmin, clock.Now()); !errIs(err, ErrInvalidTime) {
		t.Fatalf("delete with deadline == now = %v, want ErrInvalidTime", err)
	}
	if err := svc.Delete("o", IdentityAdmin, clock.Now().Add(-time.Second)); !errIs(err, ErrInvalidTime) {
		t.Fatalf("delete with past deadline = %v, want ErrInvalidTime", err)
	}
	if err := svc.Freeze("o", IdentityAdmin, 0); !errIs(err, ErrInvalidTime) {
		t.Fatalf("freeze 0 = %v, want ErrInvalidTime", err)
	}
	if err := svc.Freeze("o", IdentityAdmin, -time.Second); !errIs(err, ErrInvalidTime) {
		t.Fatalf("freeze negative = %v, want ErrInvalidTime", err)
	}
	// 被拒绝的转换不改变状态。
	if got := svc.GetView("o", IdentityAdmin); got.State != StateAlive {
		t.Fatalf("state after rejected requests = %s", got.State)
	}
}

// 保留期冻结：期满前后对撤销与归档请求的拒绝与放行。
func TestFreezeBoundaries(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)
	mustCreate(t, svc, nil, "o")

	clock.Set(t0)
	if err := svc.Freeze("o", IdentityAdmin, 10*time.Second); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	freezeEnd := t0.Add(10 * time.Second)

	// 期满前一刻：撤销、归档都被第 4 类错误拒绝。
	clock.Set(freezeEnd.Add(-1))
	if err := svc.Undo("o", IdentityAdmin); !errIs(err, ErrFrozenNotExpired) {
		t.Fatalf("undo before freeze end = %v, want ErrFrozenNotExpired", err)
	}
	if err := svc.Archive("o", IdentityAdmin); !errIs(err, ErrFrozenNotExpired) {
		t.Fatalf("archive before freeze end = %v, want ErrFrozenNotExpired", err)
	}

	// 恰好期满：显式归档放行（幂等成功），对象进入已归档。
	clock.Set(freezeEnd)
	if err := svc.Archive("o", IdentityAdmin); err != nil {
		t.Fatalf("archive exactly at freeze end: %v", err)
	}
	if got := svc.GetView("o", IdentityAdmin); got.State != StateArchived {
		t.Fatalf("state = %s, want archived", got.State)
	}

	// 第二个对象：期满后“刚过去”由普通查询惰性归档。
	mustCreate(t, svc, nil, "p")
	freezeAtP := t0.Add(10 * time.Second)
	clock.Set(freezeAtP)
	if err := svc.Freeze("p", IdentityAdmin, 5*time.Second); err != nil {
		t.Fatalf("freeze p: %v", err)
	}
	clock.Set(freezeAtP.Add(5*time.Second + 1))
	if got := svc.GetView("p", IdentityAdmin); got.State != StateArchived {
		t.Fatalf("p state = %s, want archived", got.State)
	}

	// 第三个对象：期满后撤销仍然不允许（终态，方向错误）。
	mustCreate(t, svc, nil, "q")
	clock.Set(t0)
	if err := svc.Freeze("q", IdentityAdmin, 1*time.Second); err != nil {
		t.Fatalf("freeze q: %v", err)
	}
	clock.Set(t0.Add(2 * time.Second))
	if err := svc.Undo("q", IdentityAdmin); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("undo after freeze end = %v, want ErrInvalidTransition", err)
	}
}

// 冻结时长进入时一次性确定，不因系统时钟或后续操作延长。
func TestFreezeDurationFixedAtEntry(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)
	mustCreate(t, svc, nil, "o")
	clock.Set(t0)
	if err := svc.Freeze("o", IdentityAdmin, 10*time.Second); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	want := t0.Add(10 * time.Second)

	for _, jump := range []time.Duration{3 * time.Second, 7 * time.Second, 9 * time.Second} {
		clock.Set(t0.Add(jump))
		v := svc.GetView("o", IdentityAdmin)
		if v.State != StateFrozen || !v.FreezeDeadline.Equal(want) {
			t.Fatalf("at +%v: state=%s deadline=%v, want frozen %v",
				jump, v.State, v.FreezeDeadline, want)
		}
		// 冻结期间任何非法操作都不得重算截止时刻。
		_ = svc.Freeze("o", IdentityAdmin, time.Hour)
		_ = svc.Delete("o", IdentityAdmin, want.Add(time.Hour))
		v2 := svc.GetView("o", IdentityAdmin)
		if !v2.FreezeDeadline.Equal(want) {
			t.Fatalf("deadline changed to %v, want %v", v2.FreezeDeadline, want)
		}
	}
}

// 违反固定方向的转换一律拒绝，且不改变状态。
func TestIllegalDirectionsRejected(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)

	// 不存在：第 1 类优先。
	if err := svc.Undo("ghost", IdentityAdmin); !errIs(err, ErrObjectNotFound) {
		t.Fatalf("undo missing = %v", err)
	}
	if err := svc.Freeze("ghost", IdentityAdmin, time.Second); !errIs(err, ErrObjectNotFound) {
		t.Fatalf("freeze missing = %v", err)
	}

	mustCreate(t, svc, nil, "o")

	// alive 上 undo/archive 都是非法方向。
	if err := svc.Undo("o", IdentityAdmin); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("undo alive = %v", err)
	}
	if err := svc.Archive("o", IdentityAdmin); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("archive alive = %v", err)
	}

	// 宽限期内不允许调用方提前显式归档。
	clock.Set(t0)
	if err := svc.Delete("o", IdentityAdmin, t0.Add(time.Hour)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	clockAdvanceCheck := clock.Now()
	_ = clockAdvanceCheck
	if err := svc.Archive("o", IdentityAdmin); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("archive during grace = %v, want ErrInvalidTransition", err)
	}
	if got := svc.GetView("o", IdentityAdmin); got.State != StateGrace {
		t.Fatalf("state after rejected early archive = %s", got.State)
	}

	// frozen 不允许 delete/freeze（即使时长/截止参数合法，方向错误优先于第 3 类）。
	mustCreate(t, svc, nil, "p")
	if err := svc.Freeze("p", IdentityAdmin, time.Hour); err != nil {
		t.Fatalf("freeze p: %v", err)
	}
	if err := svc.Delete("p", IdentityAdmin, t0.Add(2*time.Hour)); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("delete frozen = %v", err)
	}
	if err := svc.Freeze("p", IdentityAdmin, time.Hour); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("re-freeze frozen = %v", err)
	}
}

// 错误判定次序固定：存在性 > 方向 > 非法值 > 冻结未满。
func TestErrorOrderFixed(t *testing.T) {
	svc, _, clock, _, _ := newHarness(t)
	mustCreate(t, svc, nil, "alive")
	mustCreate(t, svc, nil, "froz")
	clock.Set(t0)
	if err := svc.Freeze("froz", IdentityAdmin, time.Hour); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	// 不存在 + 非法时长 → 仍是第 1 类。
	if err := svc.Freeze("ghost", IdentityAdmin, -time.Second); !errIs(err, ErrObjectNotFound) {
		t.Fatalf("freeze missing invalid dur = %v", err)
	}
	// alive 上方向合法但时长非法 → 第 3 类（这里 alive→freeze 合法）。
	if err := svc.Freeze("alive", IdentityAdmin, 0); !errIs(err, ErrInvalidTime) {
		t.Fatalf("freeze alive 0 = %v", err)
	}
	// frozen 上再 freeze：即使时长非法，也先报第 2 类方向错误。
	if err := svc.Freeze("froz", IdentityAdmin, 0); !errIs(err, ErrInvalidTransition) {
		t.Fatalf("re-freeze with invalid dur = %v, want ErrInvalidTransition", err)
	}
	// frozen 未满：归档报第 4 类而非第 2 类。
	if err := svc.Archive("froz", IdentityAdmin); !errIs(err, ErrFrozenNotExpired) {
		t.Fatalf("archive frozen = %v, want ErrFrozenNotExpired", err)
	}
}

// 被拒绝的转换不得修改状态、截止时刻。
func TestRejectedRequestLeavesStateUntouched(t *testing.T) {
	svc, _, clock, log, _ := newHarness(t)
	mustCreate(t, svc, nil, "o")
	clock.Set(t0)
	if err := svc.Freeze("o", IdentityAdmin, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	want := t0.Add(10 * time.Second)

	for i := 0; i < 5; i++ {
		_ = svc.Archive("o", IdentityAdmin)
		_ = svc.Undo("o", IdentityAdmin)
		_ = svc.Delete("o", IdentityAdmin, t0.Add(time.Hour))
	}
	v := svc.GetView("o", IdentityAdmin)
	if v.State != StateFrozen || !v.FreezeDeadline.Equal(want) {
		t.Fatalf("state=%s deadline=%v", v.State, v.FreezeDeadline)
	}
	// 日志中被拒绝条目 StateBefore == StateAfter。
	for _, e := range log.Entries() {
		if e.Output == "rejected" && e.StateBefore != e.StateAfter {
			t.Fatalf("rejected entry mutated state: %+v", e)
		}
	}
}
