package rollout

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, salt string, steps []Step) *Controller {
	t.Helper()
	c, err := New(salt, steps)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return c
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功, 得到错误: %v", err)
	}
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("期望错误 %v, 得到 %v", want, err)
	}
}

func checkTransitions(t *testing.T, got, want []Transition) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("转移数量不符: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("转移 %d 不符: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func checkStatus(t *testing.T, c *Controller, state State, level, percent int) {
	t.Helper()
	s, l, p := c.Status()
	if s != state || l != level || p != percent {
		t.Fatalf("Status = (%v, %d, %d), 期望 (%v, %d, %d)", s, l, p, state, level, percent)
	}
}

// 题目示例: (10%,100) (50%,200) (100%), Start(0) Pause(40) Resume(90) Tick(1000)
// 应得 0→1@150, 1→2@350, 最终 Completed。
func TestSpecExample(t *testing.T) {
	c := mustNew(t, "salt", []Step{{10, 100}, {50, 200}, {100, 0}})
	mustOK(t, c.Start(0))
	mustOK(t, c.Pause(40))
	mustOK(t, c.Resume(90))
	trs, err := c.Tick(1000)
	mustOK(t, err)
	checkTransitions(t, trs, []Transition{{0, 1, 150}, {1, 2, 350}})
	checkStatus(t, c, Completed, 2, 100)
}

// 暂停恰在到期时刻: 不推进; 恢复后到期顺延暂停时长。
func TestPauseExactlyAtDue(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {100, 0}})
	mustOK(t, c.Start(0))
	mustOK(t, c.Pause(100)) // 恰在 due=100 暂停, 不推进
	checkStatus(t, c, Paused, 0, 10)
	trs, err := c.Tick(100) // 暂停期间 Tick 无转移
	mustOK(t, err)
	checkTransitions(t, trs, nil)
	mustOK(t, c.Resume(130)) // 暂停 30ms, due 顺延为 130
	trs, err = c.Tick(130)   // 恰等于顺延后的 due 即转移
	mustOK(t, err)
	checkTransitions(t, trs, []Transition{{0, 1, 130}})
	checkStatus(t, c, Completed, 1, 100)
}

// Tick 恰等于 due 即转移, 差 1 不转移。
func TestTickBoundary(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {100, 0}})
	mustOK(t, c.Start(0))
	trs, err := c.Tick(99)
	mustOK(t, err)
	checkTransitions(t, trs, nil)
	checkStatus(t, c, Running, 0, 10)
	trs, err = c.Tick(100)
	mustOK(t, err)
	checkTransitions(t, trs, []Transition{{0, 1, 100}})
	checkStatus(t, c, Completed, 1, 100)
}

// 一次 Tick 跨越多级, 各转移时刻取各自的 due。
func TestMultiLevelCrossing(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {50, 150}, {80, 0}, {100, 0}})
	mustOK(t, c.Start(0))
	trs, err := c.Tick(1000)
	mustOK(t, err)
	checkTransitions(t, trs, []Transition{{0, 1, 100}, {1, 2, 250}, {2, 3, 250}})
	checkStatus(t, c, Completed, 3, 100)
}

// 暂停期间 Tick 无转移。
func TestTickWhilePaused(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {100, 0}})
	mustOK(t, c.Start(0))
	mustOK(t, c.Pause(50))
	trs, err := c.Tick(10000)
	mustOK(t, err)
	checkTransitions(t, trs, nil)
	checkStatus(t, c, Paused, 0, 10)
}

// 回滚后 Start 从第 0 级重新起算。
func TestRollbackThenRestart(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {100, 0}})
	mustOK(t, c.Start(0))
	mustOK(t, c.Rollback(50))
	checkStatus(t, c, RolledBack, -1, 0)
	mustOK(t, c.Start(500))
	checkStatus(t, c, Running, 0, 10)
	trs, err := c.Tick(599)
	mustOK(t, err)
	checkTransitions(t, trs, nil)
	trs, err = c.Tick(600)
	mustOK(t, err)
	checkTransitions(t, trs, []Transition{{0, 1, 600}})
}

// Completed 后可回滚; Idle 与 RolledBack 状态回滚被拒。
func TestRollbackAfterCompleted(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {100, 0}})
	mustErr(t, c.Rollback(0), ErrInvalidState) // Idle 拒绝
	mustOK(t, c.Start(0))
	trs, err := c.Tick(100)
	mustOK(t, err)
	checkTransitions(t, trs, []Transition{{0, 1, 100}})
	checkStatus(t, c, Completed, 1, 100)
	mustOK(t, c.Rollback(200))
	checkStatus(t, c, RolledBack, -1, 0)
	mustErr(t, c.Rollback(300), ErrInvalidState) // RolledBack 拒绝
}

// 全体用户样本在阶段升高时范围只增不减。
func TestMonotonicMembership(t *testing.T) {
	c := mustNew(t, "pepper", []Step{{10, 50}, {30, 50}, {60, 50}, {100, 0}})
	users := make([]string, 500)
	for i := range users {
		users[i] = fmt.Sprintf("user-%d", i)
	}
	snapshot := func() map[string]bool {
		m := make(map[string]bool, len(users))
		for _, u := range users {
			in, err := c.InRollout(u)
			mustOK(t, err)
			m[u] = in
		}
		return m
	}
	mustOK(t, c.Start(0))
	prev := snapshot()
	for now := int64(50); now <= 200; now += 50 {
		_, err := c.Tick(now)
		mustOK(t, err)
		cur := snapshot()
		for _, u := range users {
			if prev[u] && !cur[u] {
				t.Fatalf("用户 %s 在阶段升高后掉出范围", u)
			}
		}
		prev = cur
	}
	checkStatus(t, c, Completed, 3, 100)
	for _, u := range users {
		if !prev[u] {
			t.Fatalf("100%% 时用户 %s 不在范围内", u)
		}
	}
}

// 拒绝次序: 时钟回拨先于状态不符; 被拒绝的操作不改变状态与 maxNow。
func TestRejectionOrder(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {100, 0}})
	mustOK(t, c.Start(10))
	// Resume(5): 既时钟回拨又状态不符, 应报时钟回拨。
	mustErr(t, c.Resume(5), ErrClockBackwards)
	// 被拒绝后 maxNow 不变: Resume(10) 应报状态不符而非时钟。
	mustErr(t, c.Resume(10), ErrInvalidState)
	// 状态未被改变。
	checkStatus(t, c, Running, 0, 10)
	// 无转移的 Tick 也更新 maxNow。
	_, err := c.Tick(50)
	mustOK(t, err)
	mustErr(t, c.Pause(49), ErrClockBackwards)
	mustOK(t, c.Pause(50))
}

// 构造参数校验顺序: 空盐 → 级数 → 百分比范围 → 严格递增 → Hold 非负。
func TestConstructorErrors(t *testing.T) {
	cases := []struct {
		name  string
		salt  string
		steps []Step
		want  error
	}{
		{"空盐优先于级数", "", nil, ErrEmptySalt},
		{"级数不足", "s", []Step{{50, 0}}, ErrTooFewSteps},
		{"百分比为0", "s", []Step{{0, 0}, {50, 0}}, ErrPercentOutOfRange},
		{"百分比超100", "s", []Step{{10, 0}, {101, 0}}, ErrPercentOutOfRange},
		{"百分比范围优先于递增", "s", []Step{{200, 0}, {200, 0}}, ErrPercentOutOfRange},
		{"不严格递增", "s", []Step{{50, 0}, {50, 0}}, ErrPercentNotIncreasing},
		{"递减", "s", []Step{{60, 0}, {50, 0}}, ErrPercentNotIncreasing},
		{"负Hold", "s", []Step{{10, -1}, {100, 0}}, ErrNegativeHold},
		{"最后一级负Hold仍被拒", "s", []Step{{10, 0}, {100, -5}}, ErrNegativeHold},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.salt, tc.steps)
			mustErr(t, err, tc.want)
		})
	}
}

// 状态不符的操作被拒: Start/Pause/Resume 的合法状态。
func TestStateRejection(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 100}, {100, 0}})
	mustErr(t, c.Pause(0), ErrInvalidState)
	mustErr(t, c.Resume(0), ErrInvalidState)
	mustOK(t, c.Start(0))
	mustErr(t, c.Start(1), ErrInvalidState)
	mustOK(t, c.Pause(2))
	mustErr(t, c.Pause(3), ErrInvalidState)
	mustErr(t, c.Start(4), ErrInvalidState)
	mustOK(t, c.Resume(5))
	// Completed 后 Start/Pause/Resume 均被拒。
	_, err := c.Tick(106)
	mustOK(t, err)
	checkStatus(t, c, Completed, 1, 100)
	mustErr(t, c.Start(200), ErrInvalidState)
	mustErr(t, c.Pause(200), ErrInvalidState)
	mustErr(t, c.Resume(200), ErrInvalidState)
}

// InRollout 空用户被拒; 分桶与参考 FNV-1a 实现一致。
func TestInRolloutBucket(t *testing.T) {
	c := mustNew(t, "salt", []Step{{10, 100}, {100, 0}})
	if _, err := c.InRollout(""); !errors.Is(err, ErrEmptyUser) {
		t.Fatalf("空用户应返回 ErrEmptyUser, 得到 %v", err)
	}
	mustOK(t, c.Start(0))
	// 独立参考实现: 标准库等价算法手算。
	ref := func(user string) uint32 {
		h := uint32(2166136261)
		for _, b := range []byte("salt\x00" + user) {
			h ^= uint32(b)
			h *= 16777619
		}
		return h % 10000
	}
	for _, u := range []string{"alice", "bob", "carol", "用户甲", "x"} {
		got, err := c.InRollout(u)
		mustOK(t, err)
		want := ref(u) < 10*100
		if got != want {
			t.Fatalf("用户 %q: InRollout=%v, 期望 %v (b=%d)", u, got, want, ref(u))
		}
	}
}

// 并发调用: 结果等价于某个串行顺序 (配合 -race 检测数据竞争)。
func TestConcurrent(t *testing.T) {
	c := mustNew(t, "s", []Step{{10, 10}, {50, 10}, {100, 0}})
	mustOK(t, c.Start(0))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := int64(g * 1000)
			for i := int64(0); i < 100; i++ {
				now := base + i
				switch i % 5 {
				case 0:
					_, _ = c.Tick(now)
				case 1:
					_ = c.Pause(now)
				case 2:
					_ = c.Resume(now)
				case 3:
					_, _ = c.InRollout(fmt.Sprintf("u%d", g))
				default:
					c.Status()
				}
			}
		}(g)
	}
	wg.Wait()
}
