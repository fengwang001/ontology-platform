package rollout

import (
	"errors"
	"fmt"
	"testing"
)

func mustNew(t *testing.T, salt string, steps []Step) *Controller {
	t.Helper()
	c, err := New(salt, steps)
	if err != nil {
		t.Fatalf("New(%q, %v) unexpected error: %v", salt, steps, err)
	}
	return c
}

func exampleSteps() []Step {
	return []Step{{Percent: 10, HoldMs: 100}, {Percent: 50, HoldMs: 200}, {Percent: 100, HoldMs: 0}}
}

// 题目给出的例子：Start(0)、Pause(40)、Resume(90)、Tick(1000)，
// 转移 0→1 于 150、1→2 于 350，最终 Completed。
func TestSpecExample(t *testing.T) {
	c := mustNew(t, "salt", exampleSteps())
	if err := c.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Pause(40); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := c.Resume(90); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	transitions, err := c.Tick(1000)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	want := []Transition{{From: 0, To: 1, AtMs: 150}, {From: 1, To: 2, AtMs: 350}}
	if len(transitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", transitions, want)
	}
	for i := range want {
		if transitions[i] != want[i] {
			t.Fatalf("transitions[%d] = %+v, want %+v", i, transitions[i], want[i])
		}
	}
	if status := c.Status(); status.State != Completed || status.Step != 2 || status.Percent != 100 {
		t.Fatalf("status = %+v, want Completed/2/100", status)
	}
}

// 暂停恰在到期时刻不推进（Pause 不推进阶段），恢复后到期顺延暂停时长。
func TestPauseAtDueDoesNotAdvance(t *testing.T) {
	c := mustNew(t, "salt", exampleSteps())
	if err := c.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Pause(100); err != nil { // 恰好是原始 due
		t.Fatalf("Pause: %v", err)
	}
	transitions, err := c.Tick(100) // 暂停期间 Tick 无转移
	if err != nil {
		t.Fatalf("Tick while paused: %v", err)
	}
	if len(transitions) != 0 {
		t.Fatalf("paused Tick transitions = %v, want none", transitions)
	}
	if err := c.Resume(130); err != nil { // 暂停 30ms
		t.Fatalf("Resume: %v", err)
	}
	transitions, err = c.Tick(130) // 顺延后的 due 恰为 130，此时转移
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(transitions) != 1 || transitions[0] != (Transition{From: 0, To: 1, AtMs: 130}) {
		t.Fatalf("Tick(130) transitions = %v, want 0->1 at 130", transitions)
	}
}

// Tick 恰等于 due 即转移，差 1 不转移。
func TestTickDueBoundary(t *testing.T) {
	c := mustNew(t, "salt", []Step{{Percent: 1, HoldMs: 10}, {Percent: 2, HoldMs: 10}})
	if err := c.Start(5); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tr, err := c.Tick(14); err != nil || len(tr) != 0 {
		t.Fatalf("Tick(14) = %v, %v; want no transition", tr, err)
	}
	if tr, err := c.Tick(15); err != nil || len(tr) != 1 || tr[0].AtMs != 15 {
		t.Fatalf("Tick(15) = %v, %v; want 0->1 at 15", tr, err)
	}
}

// 一次 Tick 跨越多级时各转移时刻取各自的 due。
func TestTickCrossesMultipleLevels(t *testing.T) {
	c := mustNew(t, "salt", []Step{
		{Percent: 10, HoldMs: 100},
		{Percent: 20, HoldMs: 50},
		{Percent: 30, HoldMs: 25},
		{Percent: 100, HoldMs: 0},
	})
	if err := c.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tr, err := c.Tick(1000)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	want := []Transition{
		{From: 0, To: 1, AtMs: 100},
		{From: 1, To: 2, AtMs: 150},
		{From: 2, To: 3, AtMs: 175},
	}
	if len(tr) != len(want) {
		t.Fatalf("transitions = %v, want %v", tr, want)
	}
	for i := range want {
		if tr[i] != want[i] {
			t.Fatalf("transitions[%d] = %+v, want %+v", i, tr[i], want[i])
		}
	}
	if status := c.Status(); status.State != Completed {
		t.Fatalf("status = %+v, want Completed", status)
	}
}

// 回滚后 Start 从第 0 级重新起算。
func TestRestartAfterRollback(t *testing.T) {
	c := mustNew(t, "salt", exampleSteps())
	if err := c.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Tick(200); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := c.Rollback(210); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if status := c.Status(); status.State != RolledBack || status.Step != -1 || status.Percent != 0 {
		t.Fatalf("status = %+v, want RolledBack/-1/0", status)
	}
	if in, err := c.InRollout("anyone"); err != nil || in {
		t.Fatalf("InRollout after rollback = %v, %v; want false", in, err)
	}
	if err := c.Start(1000); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	if status := c.Status(); !(status.State == Running && status.Step == 0 && status.Percent == 10) {
		t.Fatalf("status = %+v, want Running/0/10", status)
	}
	tr, err := c.Tick(1099)
	if err != nil || len(tr) != 0 {
		t.Fatalf("Tick(1099) = %v, %v; want none (fresh hold starts at 1000)", tr, err)
	}
	if tr, err = c.Tick(1100); err != nil || len(tr) != 1 || tr[0].AtMs != 1100 {
		t.Fatalf("Tick(1100) = %v, %v; want 0->1 at 1100", tr, err)
	}
}

// Completed 后可以回滚；Completed 下 Tick 无转移。
func TestRollbackAfterCompleted(t *testing.T) {
	c := mustNew(t, "salt", exampleSteps())
	if err := c.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Tick(1000); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if tr, err := c.Tick(2000); err != nil || len(tr) != 0 {
		t.Fatalf("Tick after Completed = %v, %v; want none", tr, err)
	}
	if err := c.Rollback(2000); err != nil {
		t.Fatalf("Rollback after Completed: %v", err)
	}
	if status := c.Status(); status.State != RolledBack {
		t.Fatalf("status = %+v, want RolledBack", status)
	}
}

// 全体用户样本在阶段升高时范围只增不减（分桶单调性）。
func TestRangeMonotonicAcrossStages(t *testing.T) {
	c := mustNew(t, "salt", exampleSteps())
	users := make([]string, 500)
	for i := range users {
		users[i] = fmt.Sprintf("user-%d", i)
	}
	inRange := func() map[string]bool {
		in := make(map[string]bool)
		for _, u := range users {
			ok, err := c.InRollout(u)
			if err != nil {
				t.Fatalf("InRollout(%q): %v", u, err)
			}
			in[u] = ok
		}
		return in
	}
	if err := c.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	prev := map[string]bool{} // Idle: 无人在范围内
	for tick := int64(0); tick <= 400; tick += 50 {
		if _, err := c.Tick(tick); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		cur := inRange()
		for _, u := range users {
			if prev[u] && !cur[u] {
				t.Fatalf("user %q left the rollout range at tick %d", u, tick)
			}
		}
		prev = cur
	}
	for _, u := range users {
		if !prev[u] {
			t.Fatalf("user %q not in range at 100%%", u)
		}
	}
}

func TestConstructionValidation(t *testing.T) {
	cases := []struct {
		name  string
		salt  string
		steps []Step
		want  error
	}{
		{"empty salt", "", exampleSteps(), ErrEmptySalt},
		{"too few steps", "salt", []Step{{Percent: 10, HoldMs: 1}}, ErrTooFewSteps},
		{"percent below 1", "salt", []Step{{Percent: 0, HoldMs: 1}, {Percent: 10, HoldMs: 1}}, ErrPercentOutOfRange},
		{"percent above 100", "salt", []Step{{Percent: 10, HoldMs: 1}, {Percent: 101, HoldMs: 1}}, ErrPercentOutOfRange},
		{"percent not increasing", "salt", []Step{{Percent: 50, HoldMs: 1}, {Percent: 50, HoldMs: 1}}, ErrPercentNotStrictlyIncreasing},
		{"negative hold", "salt", []Step{{Percent: 10, HoldMs: 1}, {Percent: 50, HoldMs: -1}}, ErrNegativeHold},
		{"negative last hold still checked", "salt", []Step{{Percent: 10, HoldMs: 1}, {Percent: 100, HoldMs: -1}}, ErrNegativeHold},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.salt, tc.steps); !errors.Is(err, tc.want) {
				t.Fatalf("New error = %v, want %v", err, tc.want)
			}
		})
	}
}

// 拒绝次序：时钟回拨先于状态不符；被拒绝的操作不改变任何状态与暂停累计。
func TestRejectionOrderAndNoEffect(t *testing.T) {
	c := mustNew(t, "salt", exampleSteps())
	if err := c.Start(10); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Paused 下回拨：时钟错误优先于状态错误（Resume 本可通过状态检查）
	if err := c.Pause(20); err != nil {
		t.Fatalf("Pause(20): %v", err)
	}
	if err := c.Resume(15); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Resume(15) error = %v, want ErrClockRewind", err)
	}
	// 合法时钟但状态不符：Resume 在（恢复到）Running 下非法
	if err := c.Resume(40); err != nil {
		t.Fatalf("Resume(40): %v", err)
	}
	if err := c.Resume(50); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Resume while Running error = %v, want ErrInvalidState", err)
	}
	// 被拒绝的 Resume 未改变暂停累计：暂停区间仅为 [20,40]，due = 10+100+20 = 130
	tr, err := c.Tick(119)
	if err != nil || len(tr) != 0 {
		t.Fatalf("Tick(119) = %v, %v; want none", tr, err)
	}
	if tr, err = c.Tick(130); err != nil || len(tr) != 1 || tr[0].AtMs != 130 {
		t.Fatalf("Tick(130) = %v, %v; want transition at 130", tr, err)
	}
	// 无转移的 Tick 同样登记 maxNow：回拨 Tick 被拒且不改变状态
	if _, err := c.Tick(129); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Tick(129) error = %v, want ErrClockRewind", err)
	}
	if status := c.Status(); status.State != Running || status.Step != 1 {
		t.Fatalf("status after rejected tick = %+v, want Running/1", status)
	}
	// Rollback 在 Idle 被拒
	idle := mustNew(t, "salt", exampleSteps())
	if err := idle.Rollback(5); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Rollback on Idle error = %v, want ErrInvalidState", err)
	}
	if status := idle.Status(); status.State != Idle {
		t.Fatalf("status = %+v, want Idle", status)
	}
	// Rollback 后再 Rollback 被拒，回拨仍优先于状态检查
	if err := c.Rollback(200); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := c.Rollback(150); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Rollback(150) error = %v, want ErrClockRewind", err)
	}
	if err := c.Rollback(200); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Rollback on RolledBack error = %v, want ErrInvalidState", err)
	}
	// 已回滚时 Start 之外的状态不符依然先查时钟
	if err := c.Pause(199); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Pause(199) error = %v, want ErrClockRewind", err)
	}
	if err := c.Pause(200); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Pause on RolledBack error = %v, want ErrInvalidState", err)
	}
	// InRollout 空用户被拒
	if _, err := c.InRollout(""); !errors.Is(err, ErrEmptyUser) {
		t.Fatalf("InRollout(\"\") error = %v, want ErrEmptyUser", err)
	}
}
