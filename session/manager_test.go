package session

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// manualClock 是可手动推进的时钟，并发安全。
type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newManualClock() *manualClock {
	return &manualClock{t: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *manualClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func newTestManager(t *testing.T, idle, absolute time.Duration, maxPerUser int, clock *manualClock) *Manager {
	t.Helper()
	m, err := NewManager(idle, absolute, maxPerUser, WithClock(clock.now))
	if err != nil {
		t.Fatalf("NewManager(%v, %v, %d) 失败: %v", idle, absolute, maxPerUser, err)
	}
	return m
}

func mustCreate(t *testing.T, m *Manager, user string) string {
	t.Helper()
	id, err := m.Create(user)
	if err != nil {
		t.Fatalf("Create(%q) 失败: %v", user, err)
	}
	return id
}

func mustQuery(t *testing.T, m *Manager, id string) Info {
	t.Helper()
	info, err := m.Query(id)
	if err != nil {
		t.Fatalf("Query(%q) 失败: %v", id, err)
	}
	return info
}

func mustStatus(t *testing.T, m *Manager, id string) Status {
	t.Helper()
	return mustQuery(t, m, id).Status
}

// TestExpiryAtExactPoint 验证恰到点即失效：
// now == lastActive+I 时空闲超时生效，now == createdAt+A 时绝对超时生效。
func TestExpiryAtExactPoint(t *testing.T) {
	clock := newManualClock()
	m := newTestManager(t, 10*time.Second, 30*time.Second, 3, clock)

	id := mustCreate(t, m, "alice")
	t.Logf("输入: Create(alice) @t0, I=10s, A=30s -> 输出: %s", id)

	clock.advance(10*time.Second - time.Nanosecond)
	if got := mustStatus(t, m, id); got != StatusActive {
		t.Fatalf("恰到点前 1ns: 期望 active, 得到 %s", got)
	}

	clock.advance(time.Nanosecond) // 恰好 lastActive+I
	got := mustStatus(t, m, id)
	t.Logf("输入: 时钟推进至 lastActive+I; 输出: %s; 判定依据: 恰到点即失效", got)
	if got != StatusExpiredIdle {
		t.Fatalf("恰到空闲点: 期望 %s, 得到 %s", StatusExpiredIdle, got)
	}

	id2 := mustCreate(t, m, "bob")
	clock.advance(30 * time.Second) // 恰好 createdAt+A
	got = mustStatus(t, m, id2)
	t.Logf("输入: 时钟推进至 createdAt+A; 输出: %s; 判定依据: 恰到点即失效", got)
	if got != StatusExpiredAbsolute {
		t.Fatalf("恰到绝对点: 期望 %s, 得到 %s", StatusExpiredAbsolute, got)
	}
}

// TestActivityDoesNotExtendAbsolute 验证活动只刷新空闲期，不延长绝对期。
func TestActivityDoesNotExtendAbsolute(t *testing.T) {
	clock := newManualClock()
	m := newTestManager(t, 10*time.Second, 25*time.Second, 3, clock)

	id := mustCreate(t, m, "alice")
	// 每 5s 活动一次，空闲期不断刷新。
	for i := 0; i < 4; i++ {
		clock.advance(5 * time.Second)
		if err := m.Activity(id); err != nil {
			t.Fatalf("第 %d 次活动被拒绝: %v", i+1, err)
		}
	}
	t.Logf("输入: t=20s 内每 5s 活动一次; 输出: 均成功; 判定依据: 活动刷新空闲期")

	clock.advance(5 * time.Second) // t=25s == createdAt+A
	got := mustStatus(t, m, id)
	t.Logf("输入: 时钟推进至 createdAt+A; 输出: %s; 判定依据: 活动不延长绝对期", got)
	if got != StatusExpiredAbsolute {
		t.Fatalf("期望 %s, 得到 %s", StatusExpiredAbsolute, got)
	}

	err := m.Activity(id)
	var se *StateError
	if !errors.As(err, &se) || se.Status != StatusExpiredAbsolute {
		t.Fatalf("对绝对超时会话活动: 期望 StateError(%s), 得到 %v", StatusExpiredAbsolute, err)
	}
	t.Logf("输入: Activity(%s); 输出: %v; 判定依据: 非有效会话拒绝并报状态", id, err)
}

// TestBothTimeoutsAtSameInstant 验证两种超时同刻成立时报绝对超时。
func TestBothTimeoutsAtSameInstant(t *testing.T) {
	clock := newManualClock()
	m := newTestManager(t, 10*time.Second, 10*time.Second, 3, clock)

	id := mustCreate(t, m, "alice")
	clock.advance(10 * time.Second) // 同时到达 lastActive+I 与 createdAt+A
	got := mustStatus(t, m, id)
	t.Logf("输入: I==A 且时钟同刻到达两截止点; 输出: %s; 判定依据: 绝对超时优先", got)
	if got != StatusExpiredAbsolute {
		t.Fatalf("同刻成立: 期望 %s, 得到 %s", StatusExpiredAbsolute, got)
	}
}

// TestValidation 验证各类整体拒绝：空用户、非正参数、会话不存在、
// 对非有效会话活动；且被拒绝的操作不改变任何状态。
func TestValidation(t *testing.T) {
	if _, err := NewManager(0, time.Second, 1); !errors.Is(err, ErrNonPositiveIdle) {
		t.Fatalf("I 非正: 期望 %v, 得到 %v", ErrNonPositiveIdle, err)
	}
	if _, err := NewManager(time.Second, 0, 1); !errors.Is(err, ErrNonPositiveAbsolute) {
		t.Fatalf("A 非正: 期望 %v, 得到 %v", ErrNonPositiveAbsolute, err)
	}
	if _, err := NewManager(time.Second, time.Second, 0); !errors.Is(err, ErrNonPositiveMaxPerUser) {
		t.Fatalf("N 非正: 期望 %v, 得到 %v", ErrNonPositiveMaxPerUser, err)
	}
	t.Logf("输入: I/A/N 分别取非正; 输出: 三个可区分错误; 判定依据: 参数整体拒绝")

	clock := newManualClock()
	m := newTestManager(t, 10*time.Second, 30*time.Second, 2, clock)

	if _, err := m.Create(""); !errors.Is(err, ErrEmptyUser) {
		t.Fatalf("空用户创建: 期望 %v, 得到 %v", ErrEmptyUser, err)
	}
	if err := m.LogoutAll(""); !errors.Is(err, ErrEmptyUser) {
		t.Fatalf("空用户登出全部: 期望 %v, 得到 %v", ErrEmptyUser, err)
	}
	if err := m.Activity("s404"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("活动不存在会话: 期望 %v, 得到 %v", ErrSessionNotFound, err)
	}
	if _, err := m.Query("s404"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("查询不存在会话: 期望 %v, 得到 %v", ErrSessionNotFound, err)
	}
	if err := m.Logout("s404"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("登出不存在会话: 期望 %v, 得到 %v", ErrSessionNotFound, err)
	}
	t.Logf("输入: 空用户/不存在会话; 输出: ErrEmptyUser/ErrSessionNotFound; 判定依据: 可区分拒绝")

	// 对非有效会话活动：已登出、被驱逐、绝对超时、空闲超时四种状态。
	loggedOut := mustCreate(t, m, "alice")
	if err := m.Logout(loggedOut); err != nil {
		t.Fatalf("Logout 失败: %v", err)
	}
	assertActivityRejected(t, m, loggedOut, StatusLoggedOut)

	evicted := mustCreate(t, m, "alice")
	mustCreate(t, m, "alice") // 达到 N=2
	mustCreate(t, m, "alice") // 驱逐最近活动最早者，即 evicted
	if got := mustStatus(t, m, evicted); got != StatusEvicted {
		t.Fatalf("期望 %s 被驱逐, 得到 %s", evicted, got)
	}
	assertActivityRejected(t, m, evicted, StatusEvicted)

	clock2 := newManualClock()
	m2 := newTestManager(t, 5*time.Second, 10*time.Second, 2, clock2)
	idleExpired := mustCreate(t, m2, "bob")
	clock2.advance(5 * time.Second)
	assertActivityRejected(t, m2, idleExpired, StatusExpiredIdle)

	absExpired := mustCreate(t, m2, "bob")
	clock2.advance(5 * time.Second) // absExpired 创建后 5s，idle 也到点；再推进到绝对期
	clock2.advance(5 * time.Second)
	assertActivityRejected(t, m2, absExpired, StatusExpiredAbsolute)

	// 被拒绝的操作不得改变任何状态：快照前后一致。
	before := mustQuery(t, m2, absExpired)
	_ = m2.Activity(absExpired)
	after := mustQuery(t, m2, absExpired)
	if before != after {
		t.Fatalf("被拒绝的活动改变了状态: %+v -> %+v", before, after)
	}
	t.Logf("输入: 对四种非有效会话活动; 输出: 状态优先级 已登出>被驱逐>绝对>空闲; 判定依据: 拒绝且状态不变")
}

func assertActivityRejected(t *testing.T, m *Manager, id string, want Status) {
	t.Helper()
	err := m.Activity(id)
	var se *StateError
	if !errors.As(err, &se) {
		t.Fatalf("Activity(%s): 期望 *StateError, 得到 %v", id, err)
	}
	if se.Status != want {
		t.Fatalf("Activity(%s): 期望状态 %s, 得到 %s", id, want, se.Status)
	}
	t.Logf("输入: Activity(%s); 输出: %v; 判定依据: 非有效会话拒绝并报 %s", id, err, want)
}

// TestExpiredSessionsDontCountAndAreNotEvicted 验证已失效会话
// 不占名额，也不会在创建时被误改为被驱逐。
func TestExpiredSessionsDontCountAndAreNotEvicted(t *testing.T) {
	clock := newManualClock()
	m := newTestManager(t, 10*time.Second, 60*time.Second, 2, clock)

	s1 := mustCreate(t, m, "alice")
	clock.advance(10 * time.Second) // s1 空闲超时
	s2 := mustCreate(t, m, "alice") // s1 已失效不占名额
	s3 := mustCreate(t, m, "alice") // 有效数 2，仍未达驱逐条件
	t.Logf("输入: s1 失效后连续创建 %s,%s; 输出: 无驱逐; 判定依据: 失效会话不占名额", s2, s3)

	if got := mustStatus(t, m, s1); got != StatusExpiredIdle {
		t.Fatalf("s1 应保持 %s, 得到 %s", StatusExpiredIdle, got)
	}

	s4 := mustCreate(t, m, "alice") // 有效数达 N=2，驱逐 s2（最近活动最早）
	t.Logf("输入: Create 得到 %s; 输出: %s 被驱逐; 判定依据: 有效数达上限驱逐最近活动最早者", s4, s2)
	if got := mustStatus(t, m, s2); got != StatusEvicted {
		t.Fatalf("s2 应被驱逐, 得到 %s", got)
	}
	if got := mustStatus(t, m, s1); got != StatusExpiredIdle {
		t.Fatalf("s1 不得被误驱逐, 得到 %s", got)
	}
	for _, id := range []string{s3, s4} {
		if got := mustStatus(t, m, id); got != StatusActive {
			t.Fatalf("%s 应有效, 得到 %s", id, got)
		}
	}
}

// TestEvictionTieBreak 验证驱逐并列打破顺序：
// 最近活动最早 -> 创建更早 -> 标识序号小。
func TestEvictionTieBreak(t *testing.T) {
	clock := newManualClock()
	m := newTestManager(t, time.Hour, time.Hour, 2, clock)

	// 时钟冻结：s1、s2 创建时刻与最近活动完全相同。
	s1 := mustCreate(t, m, "alice")
	s2 := mustCreate(t, m, "alice")
	s3 := mustCreate(t, m, "alice") // 并列时取标识序号小者 -> 驱逐 s1
	t.Logf("输入: 时钟冻结下创建 %s,%s,%s; 输出: %s 被驱逐; 判定依据: 并列取标识序号小", s1, s2, s3, s1)
	if got := mustStatus(t, m, s1); got != StatusEvicted {
		t.Fatalf("s1 应被驱逐, 得到 %s", got)
	}

	// 推进时钟并活动 s2，使其最近活动晚于 s3。
	clock.advance(time.Second)
	if err := m.Activity(s2); err != nil {
		t.Fatalf("Activity(%s) 失败: %v", s2, err)
	}
	s4 := mustCreate(t, m, "alice") // s3 最近活动最早 -> 驱逐 s3
	t.Logf("输入: 活动 %s 后创建 %s; 输出: %s 被驱逐; 判定依据: 最近活动最早者优先", s2, s4, s3)
	if got := mustStatus(t, m, s3); got != StatusEvicted {
		t.Fatalf("s3 应被驱逐, 得到 %s", got)
	}
	for _, id := range []string{s2, s4} {
		if got := mustStatus(t, m, id); got != StatusActive {
			t.Fatalf("%s 应有效, 得到 %s", id, got)
		}
	}
}

// TestLogoutAllKeepsOtherStates 验证登出全部只改变有效会话，
// 已登出、被驱逐、已失效的会话保持原状态。
func TestLogoutAllKeepsOtherStates(t *testing.T) {
	clock := newManualClock()
	m := newTestManager(t, 10*time.Second, 60*time.Second, 2, clock)

	expired := mustCreate(t, m, "alice")
	clock.advance(10 * time.Second) // expired 空闲超时
	evicted := mustCreate(t, m, "alice")
	active1 := mustCreate(t, m, "alice")
	active2 := mustCreate(t, m, "alice") // 驱逐 evicted
	loggedOut := mustCreate(t, m, "bob")
	if err := m.Logout(loggedOut); err != nil {
		t.Fatalf("Logout 失败: %v", err)
	}

	if err := m.LogoutAll("alice"); err != nil {
		t.Fatalf("LogoutAll 失败: %v", err)
	}
	cases := []struct {
		id   string
		want Status
	}{
		{expired, StatusExpiredIdle},
		{evicted, StatusEvicted},
		{active1, StatusLoggedOut},
		{active2, StatusLoggedOut},
		{loggedOut, StatusLoggedOut},
	}
	for _, c := range cases {
		if got := mustStatus(t, m, c.id); got != c.want {
			t.Fatalf("%s: 期望 %s, 得到 %s", c.id, c.want, got)
		}
	}
	t.Logf("输入: LogoutAll(alice); 输出: 有效会话登出, 失效/被驱逐保持; 判定依据: 登出全部只改变有效会话")

	// 登出后时钟推进越过超时点，状态仍为已登出（状态优先级最高）。
	clock.advance(time.Hour)
	if got := mustStatus(t, m, active1); got != StatusLoggedOut {
		t.Fatalf("已登出会话随时钟推进应保持 logged_out, 得到 %s", got)
	}
	if got := mustStatus(t, m, evicted); got != StatusEvicted {
		t.Fatalf("被驱逐驱逐会话随时钟推进应保持 evicted, 得到 %s", got)
	}
	t.Logf("输入: 时钟推进 1h; 输出: logged_out/evicted 不变; 判定依据: 状态优先级 已登出>被驱逐>超时")
}

// TestConcurrentCreate 验证并发创建同一用户 N+1 个会话时，
// 恰驱逐其中最旧的一个，且任意串行化点上有效会话数不超过 N。
func TestConcurrentCreate(t *testing.T) {
	clock := newManualClock()
	const n = 3
	m := newTestManager(t, time.Hour, time.Hour, n, clock)

	ids := make([]string, n+1)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := m.Create("alice")
			if err != nil {
				t.Errorf("并发 Create 失败: %v", err)
				return
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()

	active, evicted := 0, 0
	var evictedID string
	for _, id := range ids {
		switch mustStatus(t, m, id) {
		case StatusActive:
			active++
		case StatusEvicted:
			evicted++
			evictedID = id
		default:
			t.Fatalf("%s 出现意外状态", id)
		}
	}
	t.Logf("输入: 并发创建 %d 个会话(N=%d); 输出: active=%d evicted=%d(%s); 判定依据: 恰驱逐最旧的一个",
		n+1, n, active, evicted, evictedID)
	if active != n || evicted != 1 {
		t.Fatalf("期望 active=%d evicted=1, 得到 active=%d evicted=%d", n, active, evicted)
	}
	// 时钟冻结，最旧者即标识序号最小者。
	if evictedID != "s1" {
		t.Fatalf("应驱逐标识序号最小者 s1, 实际驱逐 %s", evictedID)
	}
}

// TestConcurrentActivity 验证同一会话并发活动后，
// 最近活动时刻取各次时刻的最大值。
func TestConcurrentActivity(t *testing.T) {
	clock := newManualClock()
	m := newTestManager(t, time.Hour, time.Hour, 1, clock)
	id := mustCreate(t, m, "alice")
	created := mustQuery(t, m, id).CreatedAt

	const k = 8
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clock.set(created.Add(time.Duration(i+1) * time.Second))
			if err := m.Activity(id); err != nil {
				t.Errorf("并发 Activity 失败: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got := mustQuery(t, m, id).LastActiveAt
	want := created.Add(k * time.Second)
	t.Logf("输入: %d 次并发活动, 时刻 1s..%ds; 输出: lastActive=%s; 判定依据: 取各次时刻最大值",
		k, k, got)
	if !got.Equal(want) {
		t.Fatalf("最近活动应取最大值 %s, 得到 %s", want, got)
	}
}

// TestDeterminism 验证相同的操作与时钟序列得到完全相同的状态与标识。
func TestDeterminism(t *testing.T) {
	run := func() []Info {
		clock := newManualClock()
		m := newTestManager(t, 10*time.Second, 30*time.Second, 2, clock)
		var ids []string
		for _, user := range []string{"alice", "alice", "alice", "bob"} {
			ids = append(ids, mustCreate(t, m, user))
			clock.advance(3 * time.Second)
		}
		if err := m.Activity(ids[3]); err != nil {
			t.Fatalf("Activity 失败: %v", err)
		}
		clock.advance(8 * time.Second)
		if err := m.LogoutAll("alice"); err != nil {
			t.Fatalf("LogoutAll 失败: %v", err)
		}
		infos := make([]Info, len(ids))
		for i, id := range ids {
			infos[i] = mustQuery(t, m, id)
		}
		return infos
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("第 %d 个会话两次运行不一致: %+v vs %+v", i, first[i], second[i])
		}
	}
	t.Logf("输入: 相同操作与时钟序列运行两次; 输出: %s; 判定依据: 标识与状态完全一致",
		fmt.Sprint(first))
}
