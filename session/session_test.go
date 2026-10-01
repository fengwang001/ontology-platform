package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// at 返回 t0 之后 sec 秒的时刻，测试中所有时钟推进都显式给出。
func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func mustManager(t *testing.T, idleSec, absSec, limit int) *Manager {
	t.Helper()
	m, err := NewManager(time.Duration(idleSec)*time.Second, time.Duration(absSec)*time.Second, limit)
	if err != nil {
		t.Fatalf("NewManager(I=%ds, A=%ds, N=%d) 返回错误: %v", idleSec, absSec, limit, err)
	}
	return m
}

func mustCreate(t *testing.T, m *Manager, user string, sec int) string {
	t.Helper()
	id, err := m.Create(user, at(sec))
	if err != nil {
		t.Fatalf("Create(user=%q, now=t0+%ds) 返回错误: %v", user, sec, err)
	}
	t.Logf("输入: Create(user=%q, now=t0+%ds) -> 输出: id=%s (依据: 标识按全局顺序生成)", user, sec, id)
	return id
}

func mustStatus(t *testing.T, m *Manager, id string, sec int) Status {
	t.Helper()
	st, err := m.Status(id, at(sec))
	if err != nil {
		t.Fatalf("Status(id=%s, now=t0+%ds) 返回错误: %v", id, sec, err)
	}
	return st
}

func checkStatus(t *testing.T, m *Manager, id string, sec int, want Status, why string) {
	t.Helper()
	got := mustStatus(t, m, id, sec)
	t.Logf("输入: Status(id=%s, now=t0+%ds) -> 输出: %s (依据: %s)", id, sec, got, why)
	if got != want {
		t.Fatalf("Status(id=%s, now=t0+%ds) = %s, 期望 %s (依据: %s)", id, sec, got, want, why)
	}
}

// 恰到点即失效：now == lastActive+I 或 now == createdAt+A 时会话失效。
func TestExactPointExpiry(t *testing.T) {
	m := mustManager(t, 10, 1000, 5)
	id := mustCreate(t, m, "u", 0)

	checkStatus(t, m, id, 9, StatusActive, "t0+9 < lastActive(t0)+I(10s)，仍有效")
	checkStatus(t, m, id, 10, StatusExpiredIdle, "t0+10 == lastActive(t0)+I(10s)，恰到点空闲失效")

	m2 := mustManager(t, 1000, 10, 5)
	id2 := mustCreate(t, m2, "u", 0)
	checkStatus(t, m2, id2, 9, StatusActive, "t0+9 < createdAt(t0)+A(10s)，仍有效")
	checkStatus(t, m2, id2, 10, StatusExpiredAbsolute, "t0+10 == createdAt(t0)+A(10s)，恰到点绝对失效")
}

// 两种超时同刻成立时报绝对超时。
func TestBothTimeoutsSameInstantReportsAbsolute(t *testing.T) {
	m := mustManager(t, 10, 10, 5)
	id := mustCreate(t, m, "u", 0)
	checkStatus(t, m, id, 10, StatusExpiredAbsolute,
		"t0+10 同时等于 lastActive+I 与 createdAt+A，按优先级报绝对超时")
}

// 活动只推进最近活动时刻，不延长绝对期。
func TestActivityDoesNotExtendAbsolute(t *testing.T) {
	m := mustManager(t, 20, 30, 5)
	id := mustCreate(t, m, "u", 0)

	if err := m.Activity(id, at(15)); err != nil {
		t.Fatalf("Activity(id=%s, now=t0+15s) 返回错误: %v", id, err)
	}
	info, err := m.Info(id, at(15))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Activity(id=%s, now=t0+15s) -> 输出: lastActive=%v (依据: 有效会话活动成功)", id, info.LastActive)
	if !info.LastActive.Equal(at(15)) {
		t.Fatalf("lastActive = %v, 期望 %v", info.LastActive, at(15))
	}

	checkStatus(t, m, id, 29, StatusActive, "t0+29 < lastActive(t0+15)+I(20s) 且 < createdAt(t0)+A(30s)")
	checkStatus(t, m, id, 30, StatusExpiredAbsolute,
		"t0+30 == createdAt(t0)+A(30s)，活动未延长绝对期，绝对超时")
	checkStatus(t, m, id, 34, StatusExpiredAbsolute,
		"t0+34 虽小于 lastActive(t0+15)+I(20s)，但绝对期已到，仍报绝对超时")
}

// 已失效会话不占并发名额，也不会被误驱逐（保持原超时状态）。
func TestExpiredSessionsDoNotConsumeSlots(t *testing.T) {
	m := mustManager(t, 10, 1000, 1)
	s1 := mustCreate(t, m, "u", 0)

	checkStatus(t, m, s1, 10, StatusExpiredIdle, "s1 在 t0+10 已空闲失效")
	s2 := mustCreate(t, m, "u", 10)

	checkStatus(t, m, s1, 10, StatusExpiredIdle,
		"创建 s2 时 s1 已失效、不占名额，保持空闲超时而非被驱逐")
	checkStatus(t, m, s2, 10, StatusActive, "s2 新建，有效")
}

// 驱逐规则：上限已满时驱逐最近活动最早者；
// 并列取创建更早者，再并列取标识序号小者。
func TestEvictionTieBreak(t *testing.T) {
	t.Run("按最近活动时刻", func(t *testing.T) {
		m := mustManager(t, 1000, 10000, 2)
		s1 := mustCreate(t, m, "u", 0)
		s2 := mustCreate(t, m, "u", 0)
		if err := m.Activity(s2, at(5)); err != nil {
			t.Fatal(err)
		}
		s3 := mustCreate(t, m, "u", 6)
		checkStatus(t, m, s1, 6, StatusEvicted,
			"s1.lastActive(t0) < s2.lastActive(t0+5)，驱逐最近活动最早的 s1")
		checkStatus(t, m, s2, 6, StatusActive, "s2 最近活动更晚，保留")
		checkStatus(t, m, s3, 6, StatusActive, "s3 新建，有效")
	})

	t.Run("最近活动并列取创建更早", func(t *testing.T) {
		m := mustManager(t, 1000, 10000, 2)
		s1 := mustCreate(t, m, "u", 0)
		s2 := mustCreate(t, m, "u", 1)
		if err := m.Activity(s1, at(1)); err != nil {
			t.Fatal(err)
		}
		s3 := mustCreate(t, m, "u", 2)
		checkStatus(t, m, s1, 2, StatusEvicted,
			"s1 与 s2 的 lastActive 同为 t0+1，s1 创建更早(t0 < t0+1)，驱逐 s1")
		checkStatus(t, m, s2, 2, StatusActive, "s2 创建更晚，保留")
		checkStatus(t, m, s3, 2, StatusActive, "s3 新建，有效")
	})

	t.Run("再并列取标识序号小", func(t *testing.T) {
		m := mustManager(t, 1000, 10000, 2)
		s1 := mustCreate(t, m, "u", 0)
		s2 := mustCreate(t, m, "u", 0)
		s3 := mustCreate(t, m, "u", 0)
		checkStatus(t, m, s1, 0, StatusEvicted,
			"s1 与 s2 的 lastActive、createdAt 均相同，s1 序号更小，驱逐 s1")
		checkStatus(t, m, s2, 0, StatusActive, "s2 序号更大，保留")
		checkStatus(t, m, s3, 0, StatusActive, "s3 新建，有效")
	})
}

// 登出：单个登出置为已登出；登出全部只改变有效会话，
// 已失效或已被驱逐的保持原状态，且不影响其他用户。
func TestLogoutAllPreservesStates(t *testing.T) {
	m := mustManager(t, 10, 1000, 2)
	s1 := mustCreate(t, m, "u", 0)
	s2 := mustCreate(t, m, "u", 0)
	s3 := mustCreate(t, m, "u", 0) // s1 被驱逐（并列取序号小）
	other := mustCreate(t, m, "v", 0)

	if err := m.Activity(s3, at(5)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activity(other, at(5)); err != nil {
		t.Fatal(err)
	}
	checkStatus(t, m, s1, 10, StatusEvicted, "s1 已被驱逐")
	checkStatus(t, m, s2, 10, StatusExpiredIdle, "s2 在 t0+10 空闲失效")
	checkStatus(t, m, s3, 10, StatusActive, "s3 lastActive=t0+5，t0+10 仍有效")

	if err := m.LogoutAll("u", at(10)); err != nil {
		t.Fatalf("LogoutAll(user=u, now=t0+10s) 返回错误: %v", err)
	}
	t.Logf("输入: LogoutAll(user=u, now=t0+10s) -> 输出: 仅有效会话 s3 置为已登出 (依据: 已失效/被驱逐的保持原状态)")

	checkStatus(t, m, s1, 10, StatusEvicted, "s1 保持被驱逐")
	checkStatus(t, m, s2, 10, StatusExpiredIdle, "s2 保持空闲超时")
	checkStatus(t, m, s3, 10, StatusLoggedOut, "s3 有效，被登出")
	checkStatus(t, m, other, 10, StatusActive, "其他用户 v 的会话不受影响")
}

// 单个登出：有效会话置为已登出；已失效或被驱逐的保持原状态。
func TestLogoutSingle(t *testing.T) {
	m := mustManager(t, 10, 1000, 2)
	s1 := mustCreate(t, m, "u", 0)
	s2 := mustCreate(t, m, "u", 0)

	if err := m.Logout(s1, at(1)); err != nil {
		t.Fatalf("Logout(id=%s, now=t0+1s) 返回错误: %v", s1, err)
	}
	t.Logf("输入: Logout(id=%s, now=t0+1s) -> 输出: 已登出 (依据: 会话有效)", s1)
	checkStatus(t, m, s1, 1, StatusLoggedOut, "s1 有效时被登出")

	if err := m.Logout(s2, at(10)); err != nil {
		t.Fatalf("Logout(id=%s, now=t0+10s) 返回错误: %v", s2, err)
	}
	checkStatus(t, m, s2, 10, StatusExpiredIdle,
		"s2 在 t0+10 已空闲失效，登出不改变其状态")
}

// 错误拒绝：用户为空、参数非正、会话不存在、对非有效会话做活动，
// 均整体拒绝且原因可区分；被拒绝的操作不改变任何状态。
func TestRejections(t *testing.T) {
	t.Run("参数非正", func(t *testing.T) {
		for _, c := range []struct {
			name             string
			idle, abs, limit int
			wantField        string
		}{
			{"空闲期非正", 0, 10, 1, "idle"},
			{"绝对期非正", 10, -1, 1, "absolute"},
			{"上限非正", 10, 10, 0, "limit"},
		} {
			_, err := NewManager(time.Duration(c.idle)*time.Second,
				time.Duration(c.abs)*time.Second, c.limit)
			var pe *ParamError
			t.Logf("输入: NewManager(I=%ds, A=%ds, N=%d) -> 输出: err=%v (依据: %s)",
				c.idle, c.abs, c.limit, err, c.name)
			if !errors.As(err, &pe) || pe.Field != c.wantField {
				t.Fatalf("%s: err = %v, 期望 *ParamError{Field: %q}", c.name, err, c.wantField)
			}
		}
	})

	t.Run("用户为空", func(t *testing.T) {
		m := mustManager(t, 10, 100, 2)
		if _, err := m.Create("", at(0)); !errors.Is(err, ErrEmptyUser) {
			t.Fatalf("Create(空用户) err = %v, 期望 ErrEmptyUser", err)
		}
		if err := m.LogoutAll("", at(0)); !errors.Is(err, ErrEmptyUser) {
			t.Fatalf("LogoutAll(空用户) err = %v, 期望 ErrEmptyUser", err)
		}
		t.Logf("输入: Create/LogoutAll(user=\"\") -> 输出: ErrEmptyUser (依据: 用户为空整体拒绝)")
	})

	t.Run("会话不存在", func(t *testing.T) {
		m := mustManager(t, 10, 100, 2)
		if err := m.Activity("s404", at(0)); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("Activity(s404) err = %v, 期望 ErrSessionNotFound", err)
		}
		if _, err := m.Status("s404", at(0)); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("Status(s404) err = %v, 期望 ErrSessionNotFound", err)
		}
		if err := m.Logout("s404", at(0)); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("Logout(s404) err = %v, 期望 ErrSessionNotFound", err)
		}
		t.Logf("输入: Activity/Status/Logout(id=s404) -> 输出: ErrSessionNotFound (依据: 会话不存在整体拒绝)")
	})

	t.Run("对非有效会话活动", func(t *testing.T) {
		m := mustManager(t, 10, 100, 1)
		s1 := mustCreate(t, m, "u", 0)

		if err := m.Logout(s1, at(1)); err != nil {
			t.Fatal(err)
		}
		assertStateError(t, m, s1, 2, StatusLoggedOut, "已登出会话拒绝活动")

		s2 := mustCreate(t, m, "u", 2)
		s3 := mustCreate(t, m, "u", 2) // s2 被驱逐
		assertStateError(t, m, s2, 3, StatusEvicted, "被驱逐会话拒绝活动")

		// s3 在 t0+12 空闲失效（lastActive=t0+2, I=10s）
		assertStateError(t, m, s3, 12, StatusExpiredIdle, "空闲超时会话拒绝活动")

		// 绝对超时优先于空闲超时
		m2 := mustManager(t, 5, 5, 1)
		s4 := mustCreate(t, m2, "u", 0)
		assertStateError(t, m2, s4, 5, StatusExpiredAbsolute, "两种超时同刻成立报绝对超时")
	})

	t.Run("被拒绝的操作不改变状态", func(t *testing.T) {
		m := mustManager(t, 10, 100, 2)
		s1 := mustCreate(t, m, "u", 0)
		if err := m.Logout(s1, at(1)); err != nil {
			t.Fatal(err)
		}
		before, _ := m.Info(s1, at(2))
		_ = m.Activity(s1, at(2)) // 被拒绝
		after, _ := m.Info(s1, at(2))
		t.Logf("输入: Activity(已登出的 %s, now=t0+2s) -> 输出: 拒绝且 lastActive 不变 (依据: 被拒绝的操作不得改变任何状态)", s1)
		if before != after {
			t.Fatalf("被拒绝的活动改变了状态: before=%+v after=%+v", before, after)
		}
	})
}

func assertStateError(t *testing.T, m *Manager, id string, sec int, want Status, why string) {
	t.Helper()
	err := m.Activity(id, at(sec))
	var se *StateError
	t.Logf("输入: Activity(id=%s, now=t0+%ds) -> 输出: err=%v (依据: %s)", id, sec, err, why)
	if !errors.As(err, &se) || se.Status != want {
		t.Fatalf("Activity(id=%s, now=t0+%ds) err = %v, 期望 *StateError{Status: %s} (%s)",
			id, sec, err, want, why)
	}
}

// 并发创建同一用户 N+1 个会话：恰驱逐其中最旧的一个，
// 且任意串行化点上有效会话数不超过 N。
func TestConcurrentCreate(t *testing.T) {
	const n = 4
	m := mustManager(t, 1000, 10000, n)

	ids := make([]string, n+1)
	var wg sync.WaitGroup
	for i := 0; i <= n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := m.Create("u", at(0))
			if err != nil {
				t.Errorf("并发 Create 返回错误: %v", err)
				return
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()
	t.Logf("输入: 并发 Create(user=u) x %d (N=%d) -> 输出: ids=%v (依据: 恰驱逐最旧的一个)", n+1, n, ids)

	evicted, active := 0, 0
	for _, id := range ids {
		switch mustStatus(t, m, id, 0) {
		case StatusEvicted:
			evicted++
		case StatusActive:
			active++
		default:
			t.Fatalf("会话 %s 状态异常", id)
		}
	}
	if evicted != 1 || active != n {
		t.Fatalf("驱逐 %d 个、有效 %d 个，期望恰驱逐 1 个且有效 %d 个", evicted, active, n)
	}
	// 被驱逐的必为标识序号最小者（同刻创建，lastActive/createdAt 并列）
	if st := mustStatus(t, m, "s1", 0); st != StatusEvicted {
		t.Fatalf("s1 状态 = %s, 期望 evicted（并列时序号最小者被驱逐）", st)
	}
	t.Logf("判定: 驱逐 1 个且为 s1，有效 %d 个，不超过上限 N=%d", active, n)
}

// 同一会话并发活动后，最近活动取各次时刻的最大值。
func TestConcurrentActivityTakesMax(t *testing.T) {
	m := mustManager(t, 1000, 10000, 5)
	id := mustCreate(t, m, "u", 0)

	const k = 16
	var wg sync.WaitGroup
	for i := 1; i <= k; i++ {
		wg.Add(1)
		go func(sec int) {
			defer wg.Done()
			if err := m.Activity(id, at(sec)); err != nil {
				t.Errorf("并发 Activity(t0+%ds) 返回错误: %v", sec, err)
			}
		}(i)
	}
	wg.Wait()

	info, err := m.Info(id, at(k))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: 并发 Activity(id=%s, t0+1s..t0+%ds) -> 输出: lastActive=%v (依据: 取各次时刻的最大值)",
		id, k, info.LastActive)
	if !info.LastActive.Equal(at(k)) {
		t.Fatalf("lastActive = %v, 期望最大值 %v", info.LastActive, at(k))
	}
}
