package stp

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const testMaxAge = 10 * time.Second

func logReceive(t *testing.T, b *Bridge, now time.Time, port int, msg ConfigMessage) {
	t.Helper()
	err := b.Receive(now, port, msg)
	if err != nil {
		t.Logf("输入: bridge=%d receive t=%s port=%d msg=(%d,%d,%d,%d) -> 拒绝: %v",
			b.ID(), now.Format("15:04:05.000"), port, msg.RootID, msg.Cost, msg.SenderID, msg.SenderPort, err)
	} else {
		t.Logf("输入: bridge=%d receive t=%s port=%d msg=(%d,%d,%d,%d) -> 已保存",
			b.ID(), now.Format("15:04:05.000"), port, msg.RootID, msg.Cost, msg.SenderID, msg.SenderPort)
	}
}

func logRoles(t *testing.T, b *Bridge, now time.Time) (int, int, map[int]PortRole) {
	t.Helper()
	rootID, cost, statuses := b.Roles(now)
	roleByPort := make(map[int]PortRole, len(statuses))
	for _, s := range statuses {
		roleByPort[s.Port] = s.Role
	}
	t.Logf("查询: bridge=%d roles t=%s -> 输出: 根桥=%d 到根开销=%d 角色=%v",
		b.ID(), now.Format("15:04:05.000"), rootID, cost, roleByPort)
	return rootID, cost, roleByPort
}

// TestCostAddsPortCost verifies that the candidate cost is the message cost
// plus the receiving port's own path cost.
func TestCostAddsPortCost(t *testing.T) {
	b, err := NewBridge(5, []int{1, 2}, []int{3, 7}, testMaxAge)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	logReceive(t, b, now, 1, ConfigMessage{1, 4, 2, 9})
	logReceive(t, b, now, 2, ConfigMessage{1, 1, 3, 1})
	rootID, cost, roles := logRoles(t, b, now)
	if rootID != 1 || cost != 7 {
		t.Fatalf("根桥/开销 = (%d,%d), 期望 (1,7): 依据应为 4+3=7 优于 1+7=8", rootID, cost)
	}
	if roles[1] != RoleRoot {
		t.Fatalf("端口1应为根端口(开销7), 实际 %s", roles[1])
	}
	if roles[2] != RoleBlocking {
		// Advert on port 2 is (1,7,5,2), stored message is (1,1,3,1):
		// the advert is worse, so the port must block rather than advertise.
		t.Fatalf("端口2通告(1,7,5,2)不严格优于消息(1,1,3,1)，应为阻塞端口, 实际 %s", roles[2])
	}
}

// TestTieBreakSenderThenLocalPort verifies tie breaking: equal root and cost
// picks the smaller sending bridge id, and then the smaller local port id.
func TestTieBreakSenderThenLocalPort(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

	t.Run("smaller sender bridge wins", func(t *testing.T) {
		b, _ := NewBridge(9, []int{1, 2}, []int{1, 1}, testMaxAge)
		logReceive(t, b, now, 1, ConfigMessage{1, 0, 8, 3})
		logReceive(t, b, now, 2, ConfigMessage{1, 0, 2, 7})
		_, _, roles := logRoles(t, b, now)
		if roles[2] != RoleRoot {
			t.Fatalf("并列时应取发送桥编号小者: 端口2(发送桥2)应为根端口, 实际 %s/%s", roles[1], roles[2])
		}
		t.Log("判定依据: 根桥与开销并列(1,1)，发送桥 2 < 8，端口2 成为根端口")
	})

	t.Run("smaller local port wins", func(t *testing.T) {
		b, _ := NewBridge(9, []int{1, 2}, []int{1, 1}, testMaxAge)
		// Costs must match for the comparison to reach the local-port field:
		// port 1 gets message cost 1 -> total 2; port 2 gets cost 2 -> total 3.
		// Candidate port 1: (1,2,4,6,1), candidate port 2: (1,2,4,2,2):
		// sender-port would favor port 2, so instead make first four equal.
		logReceive(t, b, now, 1, ConfigMessage{1, 1, 4, 5})
		logReceive(t, b, now, 2, ConfigMessage{1, 1, 4, 5})
		_, _, roles := logRoles(t, b, now)
		if roles[1] != RoleRoot {
			t.Fatalf("前四项并列时应取本地端口编号小者: 端口1应为根端口, 实际 %s/%s", roles[1], roles[2])
		}
		t.Log("判定依据: 候选前四项完全相同，本地端口 1 < 2，端口1 成为根端口")
	})
}

// TestNewBridgeRejects verifies construction validation.
func TestNewBridgeRejects(t *testing.T) {
	cases := []struct {
		name   string
		id     int
		pid    []int
		cost   []int
		maxAge time.Duration
		want   error
	}{
		{"网桥编号非正", 0, []int{1}, []int{1}, testMaxAge, ErrNonPositiveBridgeID},
		{"无端口", 1, nil, nil, testMaxAge, ErrNoPorts},
		{"开销长度不一致", 1, []int{1, 2}, []int{1}, testMaxAge, ErrCostLength},
		{"端口开销非正", 1, []int{1, 2}, []int{1, 0}, testMaxAge, ErrNonPositivePortCost},
		{"端口编号重复", 1, []int{2, 2}, []int{1, 1}, testMaxAge, ErrDuplicatePortID},
		{"最大寿命非正", 1, []int{1}, []int{1}, 0, ErrNonPositiveMaxAge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewBridge(tc.id, tc.pid, tc.cost, tc.maxAge)
			t.Logf("输入: NewBridge id=%d ports=%v costs=%v -> 输出: %v（依据: %s）", tc.id, tc.pid, tc.cost, err, tc.name)
			if !errors.Is(err, tc.want) {
				t.Fatalf("错误 = %v, 期望 %v", err, tc.want)
			}
		})
	}
}

// TestConcurrentAccess stresses receives, link-downs and role queries against
// the same bridges with the race detector.
func TestConcurrentAccess(t *testing.T) {
	b, _ := NewBridge(2, []int{1, 2, 3}, []int{1, 1, 1}, time.Hour)
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	msgs := []ConfigMessage{
		{1, 0, 3, 1},
		{1, 2, 4, 2},
		{1, 1, 5, 1},
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(3)
		i := i
		go func() {
			defer wg.Done()
			port := 1 + i%3
			_ = b.Receive(t0.Add(time.Duration(i)*time.Second), port, msgs[i%len(msgs)])
		}()
		go func() {
			defer wg.Done()
			if i%5 == 0 {
				_ = b.LinkDown(1 + i%3)
			}
		}()
		go func() {
			defer wg.Done()
			_, _, roles := b.Roles(t0.Add(time.Duration(i) * time.Second))
			rootPorts := 0
			for _, s := range roles {
				if s.Role == RoleRoot {
					rootPorts++
				}
			}
			if rootPorts > 1 {
				t.Errorf("任一时刻根端口至多一个, 实际 %d", rootPorts)
			}
		}()
	}
	wg.Wait()
	t.Log("并发判定依据: 所有操作经同一把互斥锁串行化，go test -race 无数据竞争，且每次查询根端口 <= 1")
}

// TestDeterministicReplay replays the same timed operation sequence twice and
// requires byte-identical results.
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		kind string
		at   time.Duration
		port int
		msg  ConfigMessage
	}
	sequence := []op{
		{"recv", 1 * time.Second, 1, ConfigMessage{1, 0, 9, 1}},
		{"recv", 2 * time.Second, 2, ConfigMessage{1, 1, 4, 2}},
		{"query", 3 * time.Second, 0, ConfigMessage{}},
		{"down", 4 * time.Second, 1, ConfigMessage{}},
		{"query", 5 * time.Second, 0, ConfigMessage{}},
		{"recv", 6 * time.Second, 1, ConfigMessage{1, 2, 7, 3}},
		{"query", 7 * time.Second, 0, ConfigMessage{}},
	}
	run := func() string {
		b, _ := NewBridge(8, []int{1, 2}, []int{3, 5}, testMaxAge)
		var out string
		for _, o := range sequence {
			now := time.Time{}.Add(o.at)
			switch o.kind {
			case "recv":
				err := b.Receive(now, o.port, o.msg)
				out += fmt.Sprintf("recv:%v;", err)
			case "down":
				out += fmt.Sprintf("down:%v;", b.LinkDown(o.port))
			case "query":
				rootID, cost, roles := b.Roles(now)
				out += fmt.Sprintf("query=%d,%d,%v;", rootID, cost, roles)
			}
		}
		return out
	}
	first, second := run(), run()
	t.Logf("重放输出: %s", first)
	if first != second {
		t.Fatalf("相同操作序列重放结果不一致:\n%s\n%s", first, second)
	}
}

// TestDesignatedVsBlocking verifies the strict-better comparison: a port is
// designated only when the locally advertised vector is strictly smaller than
// the stored message; an equal-or-worse ordering yields blocking.
func TestDesignatedVsBlocking(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	b, _ := NewBridge(3, []int{1, 2, 3}, []int{1, 1, 1}, testMaxAge)
	logReceive(t, b, now, 1, ConfigMessage{1, 0, 1, 1})
	logReceive(t, b, now, 2, ConfigMessage{1, 5, 2, 1})
	// First three components equal the advert (1,1,3): the advert is not
	// strictly better, so port 3 must block.
	// Equal at component 3 (sender bridge id 3) is impossible via a real peer
	// (sending bridge == self is rejected), so use sender id 2 with equal root
	// and cost components: advert (1,1,3,3) vs message (1,1,2,9) is worse at
	// component 3, still demonstrating that "equal prefix, not strictly less"
	// blocks. The fully-equal case is covered by sender id 3 in the rejection
	// ordering test being forbidden.
	logReceive(t, b, now, 3, ConfigMessage{1, 1, 2, 9})
	rootID, cost, roles := logRoles(t, b, now)
	if rootID != 1 || cost != 1 {
		t.Fatalf("根桥/开销 = (%d,%d), 期望 (1,1)", rootID, cost)
	}
	if roles[1] != RoleRoot {
		t.Fatalf("端口1应为根端口, 实际 %s", roles[1])
	}
	if roles[2] != RoleDesignated {
		t.Fatalf("端口2: 通告 (1,1,3,2) 严格优于消息 (1,5,2,1)，应为指定端口, 实际 %s", roles[2])
	}
	if roles[3] != RoleBlocking {
		t.Fatalf("端口3: 通告 (1,1,3,3) 不严格优于消息 (1,1,2,9)（第三项 3>2），应为阻塞端口, 实际 %s", roles[3])
	}
	t.Log("判定依据: 严格优于要求四项依次严格更小；不更小即阻塞 -> 端口2 指定，端口3 阻塞")

	t.Log("输入: link-down port 2")
	if err := b.LinkDown(2); err != nil {
		t.Fatal(err)
	}
	_, _, roles = logRoles(t, b, now)
	if roles[2] != RoleDesignated {
		t.Fatalf("端口2断链后无消息，应为指定端口, 实际 %s", roles[2])
	}
}

// TestExpiryReclaimsRoot verifies that once the root message reaches max age
// the bridge considers itself root again and all ports become designated.
func TestExpiryReclaimsRoot(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	b, _ := NewBridge(5, []int{1, 2}, []int{2, 4}, testMaxAge)
	logReceive(t, b, t0, 1, ConfigMessage{1, 0, 1, 1})
	rootID, cost, roles := logRoles(t, b, t0)
	if rootID != 1 || cost != 2 || roles[1] != RoleRoot || roles[2] != RoleDesignated {
		t.Fatalf("初始计算错误: root=%d cost=%d roles=%v", rootID, cost, roles)
	}

	justBefore := t0.Add(testMaxAge - time.Nanosecond)
	rootID, _, roles = logRoles(t, b, justBefore)
	t.Logf("判定依据: now-arrived=%s < A=%s，消息仍有效，根桥仍为 %d", testMaxAge-time.Nanosecond, testMaxAge, rootID)
	if rootID != 1 || roles[1] != RoleRoot {
		t.Fatal("临界时刻前消息不应失效")
	}

	expired := t0.Add(testMaxAge)
	rootID, cost, roles = logRoles(t, b, expired)
	if rootID != 5 || cost != 0 {
		t.Fatalf("消息过期(now-arrived>=A)后应重新自认根桥且开销为0, 实际 root=%d cost=%d", rootID, cost)
	}
	for pid, role := range roles {
		if role != RoleDesignated {
			t.Fatalf("根桥 %d 的端口 %d 必须全部为指定端口, 实际 %s", rootID, pid, role)
		}
	}
	t.Logf("判定依据: now-arrived=%s >= A=%s，消息失效；无更优候选 -> 本桥 5 自认根桥，端口全为指定", testMaxAge, testMaxAge)
}

// TestReceiveRejectionOrder verifies only the first violated rule is reported
// and rejected calls never mutate stored messages.
func TestReceiveRejectionOrder(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	b, _ := NewBridge(3, []int{1, 2}, []int{1, 1}, testMaxAge)
	logReceive(t, b, t0, 1, ConfigMessage{1, 0, 2, 1})

	cases := []struct {
		name string
		now  time.Time
		port int
		msg  ConfigMessage
		want error
	}{
		{"时钟回拨最先报", t0.Add(-time.Second), 1, ConfigMessage{1, 0, 2, 1}, ErrClockRollback},
		{"时钟回拨优先于端口不存在", t0.Add(-time.Second), 99, ConfigMessage{1, 0, 2, 1}, ErrClockRollback},
		{"端口不存在", t0, 99, ConfigMessage{1, 0, 2, 1}, ErrPortNotFound},
		{"端口不存在优先于非法桥编号", t0, 99, ConfigMessage{0, 0, 2, 1}, ErrPortNotFound},
		{"根桥编号非正", t0, 1, ConfigMessage{0, 0, 2, 1}, ErrInvalidBridgeID},
		{"发送桥编号非正", t0, 1, ConfigMessage{1, 0, 0, 1}, ErrInvalidBridgeID},
		{"非法桥编号优先于负开销", t0, 1, ConfigMessage{0, -1, 2, 1}, ErrInvalidBridgeID},
		{"开销为负", t0, 1, ConfigMessage{1, -1, 2, 1}, ErrNegativeCost},
		{"负开销优先于发送桥为本桥", t0, 1, ConfigMessage{1, -1, 3, 1}, ErrNegativeCost},
		{"发送桥为本桥", t0, 1, ConfigMessage{1, 0, 3, 1}, ErrSenderIsSelf},
		{"自发自收优先于发送端口非法", t0, 1, ConfigMessage{1, 0, 3, 0}, ErrSenderIsSelf},
		{"发送端口非正", t0, 1, ConfigMessage{1, 0, 2, 0}, ErrInvalidSenderPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Receive(tc.now, tc.port, tc.msg)
			t.Logf("输入: receive t=%s port=%d msg=(%d,%d,%d,%d) -> 输出: %v（依据: %s）",
				tc.now.Format("15:04:05"), tc.port, tc.msg.RootID, tc.msg.Cost, tc.msg.SenderID, tc.msg.SenderPort, err, tc.name)
			if !errors.Is(err, tc.want) {
				t.Fatalf("错误 = %v, 期望 %v", err, tc.want)
			}
		})
	}

	rootID, cost, roles := logRoles(t, b, t0)
	if rootID != 1 || cost != 1 || roles[1] != RoleRoot {
		t.Fatalf("被拒绝操作不得改变已保存消息, 实际 root=%d cost=%d roles=%v", rootID, cost, roles)
	}

	t.Run("new message replaces old", func(t *testing.T) {
		logReceive(t, b, t0.Add(time.Second), 1, ConfigMessage{2, 0, 4, 1})
		rootID, _, _ := logRoles(t, b, t0.Add(time.Second))
		if rootID != 2 {
			t.Fatalf("新消息应直接替换旧消息, 实际根桥=%d", rootID)
		}
	})

	t.Run("link down rejection and clearing", func(t *testing.T) {
		if err := b.LinkDown(99); !errors.Is(err, ErrPortNotFound) {
			t.Fatalf("断链不存在端口应拒绝, 实际 %v", err)
		}
		t.Log("输入: link-down port=99 -> 输出: 拒绝（端口不存在）")
		if err := b.LinkDown(1); err != nil {
			t.Fatal(err)
		}
		t.Log("输入: link-down port=1 -> 输出: 已清除保存的消息")
		rootID, cost, _ := logRoles(t, b, t0.Add(time.Second))
		if rootID != 3 || cost != 0 {
			t.Fatalf("断链清除消息后应自认根桥, 实际 root=%d cost=%d", rootID, cost)
		}
	})
}
