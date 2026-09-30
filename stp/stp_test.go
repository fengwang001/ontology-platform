package stp

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, id, maxAge int64, specs ...PortSpec) *Bridge {
	t.Helper()
	b, err := NewBridge(id, maxAge, specs)
	if err != nil {
		t.Fatalf("NewBridge(%d): %v", id, err)
	}
	return b
}

func mustReceive(t *testing.T, b *Bridge, now Time, port int64, msg ConfigMessage) {
	t.Helper()
	t.Logf("输入: Receive(now=%d, port=%d, msg=(root=%d,cost=%d,sender=%d,sendPort=%d))",
		now, port, msg.RootID, msg.Cost, msg.SenderID, msg.PortID)
	if err := b.Receive(now, port, msg); err != nil {
		t.Fatalf("Receive: %v", err)
	}
}

func roleOf(r RolesResult, port int64) PortResult {
	for _, p := range r.Ports {
		if p.PortID == port {
			return p
		}
	}
	return PortResult{PortID: port, Role: -1}
}

func rootPort(r RolesResult) int64 {
	for _, p := range r.Ports {
		if p.Role == RoleRoot {
			return p.PortID
		}
	}
	return -1
}

func logRoles(t *testing.T, r RolesResult, reason string) {
	t.Helper()
	t.Logf("输出: 网桥=%d 根桥=%d 到根开销=%d；判定依据: %s",
		r.BridgeID, r.RootID, r.RootCost, reason)
	for _, p := range r.Ports {
		t.Logf("  端口=%d 角色=%s 转发=%v 有有效消息=%v", p.PortID, p.Role, p.Forward, p.HasMessage)
	}
}

// 候选到根开销取消息开销加本端口路径开销。
func TestRootCostAddsLocalPortCost(t *testing.T) {
	b := mustNew(t, 5, 10, PortSpec{ID: 1, Cost: 7}, PortSpec{ID: 2, Cost: 3})
	mustReceive(t, b, 1, 1, ConfigMessage{RootID: 4, Cost: 100, SenderID: 4, PortID: 9})
	mustReceive(t, b, 1, 2, ConfigMessage{RootID: 4, Cost: 102, SenderID: 4, PortID: 9})

	r, err := b.Roles(1)
	if err != nil {
		t.Fatal(err)
	}
	// 端口1候选开销 100+7=107；端口2候选开销 102+3=105，端口2胜。
	if r.RootID != 4 || r.RootCost != 105 {
		t.Fatalf("root=%d cost=%d, want 4/105", r.RootID, r.RootCost)
	}
	if roleOf(r, 1).Role != RoleBlocking || roleOf(r, 2).Role != RoleRoot {
		t.Fatalf("roles: p1=%v p2=%v", roleOf(r, 1).Role, roleOf(r, 2).Role)
	}
	logRoles(t, r, "候选 (4,107,..,本地1) 与 (4,105,..,本地2)，按到根开销 105<107 选端口2为根端口；端口1通告(4,105,5,1)不优于消息(4,100,4,9)，阻塞")
}

// 到根开销并列时依次取小的发送桥、发送端口、本地端口。
func TestRootPortTieBreakers(t *testing.T) {
	t.Run("sender bridge", func(t *testing.T) {
		b := mustNew(t, 8, 10, PortSpec{ID: 1, Cost: 10}, PortSpec{ID: 2, Cost: 10})
		mustReceive(t, b, 1, 1, ConfigMessage{RootID: 1, Cost: 0, SenderID: 7, PortID: 1})
		mustReceive(t, b, 1, 2, ConfigMessage{RootID: 1, Cost: 0, SenderID: 6, PortID: 9})
		r, _ := b.Roles(1)
		if rp := rootPort(r); rp != 2 {
			t.Fatalf("root port=%d, want 2 (sender 6<7)", rp)
		}
		logRoles(t, r, "根桥与到根开销相同，发送桥 6<7，端口2为根端口")
	})

	t.Run("sender port", func(t *testing.T) {
		b := mustNew(t, 8, 10, PortSpec{ID: 1, Cost: 10}, PortSpec{ID: 2, Cost: 10})
		mustReceive(t, b, 1, 1, ConfigMessage{RootID: 1, Cost: 0, SenderID: 6, PortID: 5})
		mustReceive(t, b, 1, 2, ConfigMessage{RootID: 1, Cost: 0, SenderID: 6, PortID: 2})
		r, _ := b.Roles(1)
		if rp := rootPort(r); rp != 2 {
			t.Fatalf("root port=%d, want 2 (sender port 2<5)", rp)
		}
		logRoles(t, r, "前三项相同，发送端口 2<5，端口2为根端口")
	})

	t.Run("local port", func(t *testing.T) {
		b := mustNew(t, 8, 10, PortSpec{ID: 1, Cost: 10}, PortSpec{ID: 2, Cost: 10})
		mustReceive(t, b, 1, 1, ConfigMessage{RootID: 1, Cost: 0, SenderID: 6, PortID: 4})
		mustReceive(t, b, 1, 2, ConfigMessage{RootID: 1, Cost: 0, SenderID: 6, PortID: 4})
		r, _ := b.Roles(1)
		if rp := rootPort(r); rp != 1 {
			t.Fatalf("root port=%d, want 1 (local port 1<2)", rp)
		}
		logRoles(t, r, "前四项相同，本地端口 1<2，端口1为根端口")
	})
}

// 指定/阻塞的严格优于判定：相等不算优。
func TestDesignatedVsBlocking(t *testing.T) {
	b := mustNew(t, 3, 10,
		PortSpec{ID: 1, Cost: 10},
		PortSpec{ID: 2, Cost: 10},
		PortSpec{ID: 3, Cost: 10},
		PortSpec{ID: 4, Cost: 10},
		PortSpec{ID: 5, Cost: 10},
		PortSpec{ID: 6, Cost: 10},
	)
	mustReceive(t, b, 0, 1, ConfigMessage{RootID: 1, Cost: 0, SenderID: 1, PortID: 1})
	// 端口2：本桥通告 (1,10,3,2)，消息 (1,10,2,2)，通告发送桥更大 => 阻塞。
	mustReceive(t, b, 0, 2, ConfigMessage{RootID: 1, Cost: 10, SenderID: 2, PortID: 2})
	// 端口3：通告 (1,10,3,3)，消息 (1,9,..) 到根开销更小 => 不严格优 => 阻塞。
	mustReceive(t, b, 0, 3, ConfigMessage{RootID: 1, Cost: 9, SenderID: 2, PortID: 9})
	// 端口4：消息 (1,10,2,9)，通告发送桥 3>2 => 阻塞。
	mustReceive(t, b, 0, 4, ConfigMessage{RootID: 1, Cost: 10, SenderID: 2, PortID: 9})
	// 端口5：消息 (1,11,..) 到根开销更大，通告第二项 10<11 => 严格优 => 指定。
	mustReceive(t, b, 0, 5, ConfigMessage{RootID: 1, Cost: 11, SenderID: 2, PortID: 1})
	// 端口6：无消息 => 指定。

	r, err := b.Roles(0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]PortRole{
		1: RoleRoot, 2: RoleBlocking, 3: RoleBlocking, 4: RoleBlocking, 5: RoleDesignated, 6: RoleDesignated,
	}
	for _, p := range r.Ports {
		if p.Role != want[p.PortID] {
			t.Fatalf("port %d role=%v want %v", p.PortID, p.Role, want[p.PortID])
		}
		if p.Role == RoleRoot && !p.Forward {
			t.Fatalf("root port %d must forward", p.PortID)
		}
		if p.Role == RoleBlocking && p.Forward {
			t.Fatalf("blocking port %d must not forward", p.PortID)
		}
	}
	logRoles(t, r, "通告向量严格优于消息才为指定；相等或更差为阻塞；无消息为指定")
}

// 直接验证四元组的严格优于（词典序）判定，包括完全相等不优。
func TestBetterVectorStrict(t *testing.T) {
	cases := []struct {
		a, b ConfigMessage
		want bool
		why  string
	}{
		{ConfigMessage{1, 10, 3, 5}, ConfigMessage{1, 10, 3, 9}, true, "第四项 5<9"},
		{ConfigMessage{1, 10, 2, 9}, ConfigMessage{1, 10, 3, 1}, true, "第三项 2<3"},
		{ConfigMessage{1, 9, 9, 9}, ConfigMessage{1, 10, 1, 1}, true, "第二项 9<10"},
		{ConfigMessage{1, 11, 1, 1}, ConfigMessage{1, 10, 9, 9}, false, "第二项 11>10"},
		{ConfigMessage{1, 10, 3, 3}, ConfigMessage{1, 10, 3, 3}, false, "完全相等不是严格优"},
		{ConfigMessage{2, 0, 1, 1}, ConfigMessage{1, 0, 1, 1}, false, "根桥编号更大"},
	}
	for _, c := range cases {
		got := betterVector(c.a, c.b)
		t.Logf("输入: 比较 %v 与 %v -> 输出: %v；判定依据: %s", c.a, c.b, got, c.why)
		if got != c.want {
			t.Fatalf("betterVector(%v,%v)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

// 根桥消息过期后重新自认根桥。
func TestExpiredMessageRevivesRoot(t *testing.T) {
	b := mustNew(t, 2, 10, PortSpec{ID: 1, Cost: 5})
	mustReceive(t, b, 0, 1, ConfigMessage{RootID: 1, Cost: 100, SenderID: 1, PortID: 1})

	r, _ := b.Roles(0)
	logRoles(t, r, "消息有效，根桥=1，到根开销=100+5=105，端口1为根端口")
	if r.RootID != 1 || r.RootCost != 105 || roleOf(r, 1).Role != RoleRoot {
		t.Fatalf("at t=0: %+v", r)
	}

	r, _ = b.Roles(9)
	t.Logf("输入: Roles(now=9)；now-at=9 < A=10，消息仍有效，根桥仍为1")
	if r.RootID != 1 || !roleOf(r, 1).HasMessage {
		t.Fatalf("at t=9 message should still be fresh")
	}

	r, _ = b.Roles(10)
	logRoles(t, r, "now-at=10 >= A=10，消息失效不参与计算；无更优候选，本桥自认根桥，开销0，端口全指定")
	if r.RootID != 2 || r.RootCost != 0 {
		t.Fatalf("at t=10 root=%d cost=%d, want 2/0", r.RootID, r.RootCost)
	}
	if p := roleOf(r, 1); p.Role != RoleDesignated || p.HasMessage {
		t.Fatalf("at t=10 port1=%+v, want designated with no fresh message", p)
	}

	// 新消息替换旧消息，到达时刻刷新。
	mustReceive(t, b, 15, 1, ConfigMessage{RootID: 1, Cost: 50, SenderID: 1, PortID: 1})
	r, _ = b.Roles(15)
	if r.RootID != 1 || r.RootCost != 55 {
		t.Fatalf("after replacement: root=%d cost=%d", r.RootID, r.RootCost)
	}
	logRoles(t, r, "t=15 新消息直接替换旧消息，到根开销=50+5=55")
}

// 三个网桥成环收敛后恰有一个阻塞端口。
func TestThreeBridgeRingConverges(t *testing.T) {
	b1 := mustNew(t, 1, 1000, PortSpec{ID: 2, Cost: 10}, PortSpec{ID: 3, Cost: 10})
	b2 := mustNew(t, 2, 1000, PortSpec{ID: 1, Cost: 10}, PortSpec{ID: 3, Cost: 10})
	b3 := mustNew(t, 3, 1000, PortSpec{ID: 1, Cost: 10}, PortSpec{ID: 2, Cost: 10})
	bridges := map[int64]*Bridge{1: b1, 2: b2, 3: b3}

	type link struct {
		a, pa, b, pb int64
	}
	links := []link{
		{1, 2, 2, 1},
		{1, 3, 3, 1},
		{2, 3, 3, 2},
	}

	// 在每条链路上，由指定端口一侧向对端注入自己的通告向量。
	for round := Time(1); round <= 10; round++ {
		for _, l := range links {
			ra, err := bridges[l.a].Roles(round)
			if err != nil {
				t.Fatal(err)
			}
			rb, err := bridges[l.b].Roles(round)
			if err != nil {
				t.Fatal(err)
			}
			if pa := roleOf(ra, l.pa); pa.Role == RoleDesignated {
				msg := ConfigMessage{RootID: ra.RootID, Cost: ra.RootCost, SenderID: l.a, PortID: l.pa}
				t.Logf("输入: 网桥%d 端口%d 指定 -> 网桥%d.Receive(port=%d, msg=(%d,%d,%d,%d))",
					l.a, l.pa, l.b, l.pb, msg.RootID, msg.Cost, msg.SenderID, msg.PortID)
				if err := bridges[l.b].Receive(round, l.pb, msg); err != nil {
					t.Fatal(err)
				}
			}
			if pb := roleOf(rb, l.pb); pb.Role == RoleDesignated {
				msg := ConfigMessage{RootID: rb.RootID, Cost: rb.RootCost, SenderID: l.b, PortID: l.pb}
				t.Logf("输入: 网桥%d 端口%d 指定 -> 网桥%d.Receive(port=%d, msg=(%d,%d,%d,%d))",
					l.b, l.pb, l.a, l.pa, msg.RootID, msg.Cost, msg.SenderID, msg.PortID)
				if err := bridges[l.a].Receive(round, l.pa, msg); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	var blocking, rootPorts int
	for id := int64(1); id <= 3; id++ {
		r, err := bridges[id].Roles(11)
		if err != nil {
			t.Fatal(err)
		}
		logRoles(t, r, fmt.Sprintf("网桥%d 收敛结果", id))
		if r.RootID != 1 {
			t.Fatalf("bridge %d root=%d, want 1", id, r.RootID)
		}
		rp := 0
		for _, p := range r.Ports {
			switch p.Role {
			case RoleBlocking:
				blocking++
			case RoleRoot:
				rootPorts++
				rp++
				if !p.Forward {
					t.Fatalf("bridge %d root port not forwarding", id)
				}
			case RoleDesignated:
				if !p.Forward {
					t.Fatalf("bridge %d designated port not forwarding", id)
				}
			}
		}
		if id != 1 && rp != 1 {
			t.Fatalf("non-root bridge %d has %d root ports, want 1", id, rp)
		}
		if id == 1 && rp != 0 {
			t.Fatalf("root bridge %d has a root port", id)
		}
	}
	if blocking != 1 {
		t.Fatalf("blocking ports=%d, want exactly 1 in the ring", blocking)
	}
	if rootPorts != 2 {
		t.Fatalf("root ports=%d, want 2 (one per non-root bridge)", rootPorts)
	}
	t.Logf("输出: 全环阻塞端口数=%d（恰为1），根端口数=%d；判定依据: 网桥1为根桥，链路(2,3)上两端口通告相等，发送桥较小的网桥2侧为指定，网桥3端口2阻塞",
		blocking, rootPorts)
}

// 拒绝顺序：时钟回拨 > 端口不存在 > 字段非法 > 发送桥为本桥；拒绝不改状态。
func TestRejectionOrderAndNoStateChange(t *testing.T) {
	b := mustNew(t, 5, 10, PortSpec{ID: 1, Cost: 1})
	mustReceive(t, b, 10, 1, ConfigMessage{RootID: 1, Cost: 0, SenderID: 1, PortID: 1})

	good := ConfigMessage{RootID: 1, Cost: 0, SenderID: 2, PortID: 1}
	cases := []struct {
		name string
		now  Time
		port int64
		msg  ConfigMessage
		want error
	}{
		{"clock rollback wins over all", 9, 99, ConfigMessage{RootID: 0, Cost: -1, SenderID: 5}, ErrClockRolledBack},
		{"unknown port before invalid fields", 10, 99, ConfigMessage{RootID: 0, Cost: -1, SenderID: 0}, ErrUnknownPort},
		{"invalid fields before self origin", 10, 1, ConfigMessage{RootID: 0, Cost: -1, SenderID: 5}, ErrInvalidMessage},
		{"self origin", 10, 1, ConfigMessage{RootID: 1, Cost: 0, SenderID: 5, PortID: 1}, ErrSelfOrigin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := b.Receive(c.now, c.port, c.msg)
			t.Logf("输入: Receive(now=%d, port=%d, msg=(%d,%d,%d,%d)) -> 输出: %v；判定依据: %s",
				c.now, c.port, c.msg.RootID, c.msg.Cost, c.msg.SenderID, c.msg.PortID, err, c.want)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
		})
	}

	// 被拒绝的操作未改变保存的消息与时钟：旧消息仍在，且 t=10 时钟未被回拨操作污染。
	if err := b.Receive(10, 1, good); err != nil {
		t.Fatalf("state changed by rejected ops: %v", err)
	}
	r, _ := b.Roles(10)
	if p := roleOf(r, 1); p.Message != good || !p.HasMessage {
		t.Fatalf("stored message=%+v, want replacement by good msg", p.Message)
	}
	logRoles(t, r, "拒绝操作均未落盘；随后合法消息替换成功")

	// 断链：先查时钟，再查端口；端口不存在拒绝且不影响时钟。
	if err := b.LinkDown(9, 1); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("linkdown rollback: %v", err)
	}
	if err := b.LinkDown(10, 99); !errors.Is(err, ErrUnknownPort) {
		t.Fatalf("linkdown unknown port: %v", err)
	}
	if err := b.LinkDown(10, 1); err != nil {
		t.Fatalf("linkdown: %v", err)
	}
	r, _ = b.Roles(10)
	if roleOf(r, 1).HasMessage {
		t.Fatalf("message should be cleared after link down")
	}
	logRoles(t, r, "断链清除端口1保存的消息，本桥重新自认根桥")
}

func TestNewBridgeRejectsInvalidSpec(t *testing.T) {
	cases := []struct {
		name   string
		id     int64
		maxAge int64
		ports  []PortSpec
	}{
		{"non-positive bridge id", 0, 10, []PortSpec{{1, 1}}},
		{"non-positive max age", 1, 0, []PortSpec{{1, 1}}},
		{"non-positive port id", 1, 10, []PortSpec{{0, 1}}},
		{"non-positive port cost", 1, 10, []PortSpec{{1, 0}}},
		{"duplicate port ids", 1, 10, []PortSpec{{1, 1}, {1, 2}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewBridge(c.id, c.maxAge, c.ports); !errors.Is(err, ErrInvalidBridge) {
				t.Fatalf("err=%v want ErrInvalidBridge", err)
			}
		})
	}
}

// 并发调用安全；相同操作序列重放结果相同。
func TestConcurrentSafeAndDeterministicReplay(t *testing.T) {
	build := func() *Bridge {
		b := mustNew(t, 4, 1000,
			PortSpec{ID: 1, Cost: 5},
			PortSpec{ID: 2, Cost: 5},
			PortSpec{ID: 3, Cost: 5},
		)
		mustReceive(t, b, 0, 1, ConfigMessage{RootID: 1, Cost: 0, SenderID: 1, PortID: 1})
		mustReceive(t, b, 1, 2, ConfigMessage{RootID: 2, Cost: 10, SenderID: 2, PortID: 1})
		return b
	}

	b := build()
	var wg sync.WaitGroup
	var next int64 = 10
	var ops int64
	var sched sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				port := int64(1 + g%3)
				// 时钟在“分配并执行”临界区内单调推进，模拟真实单调时钟。
				sched.Lock()
				now := Time(atomic.AddInt64(&next, 1))
				var err error
				if j%37 == 0 {
					err = b.LinkDown(now, port)
				} else {
					msg := ConfigMessage{RootID: 1, Cost: int64(j % 50), SenderID: int64(2 + g%2), PortID: port}
					err = b.Receive(now, port, msg)
				}
				if err == nil {
					_, err = b.Roles(now)
				}
				sched.Unlock()
				if err != nil {
					t.Errorf("concurrent op g=%d j=%d: %v", g, j, err)
					return
				}
				atomic.AddInt64(&ops, 1)
			}
		}(i)
	}
	wg.Wait()
	if ops != 16*200 {
		t.Fatalf("ops=%d", ops)
	}

	// 不变量：每个端口恰有一种角色，根端口至多一个。
	r, err := b.Roles(Time(atomic.LoadInt64(&next)))
	if err != nil {
		t.Fatal(err)
	}
	rp := 0
	for _, p := range r.Ports {
		if p.Role != RoleRoot && p.Role != RoleDesignated && p.Role != RoleBlocking {
			t.Fatalf("invalid role %v on port %d", p.Role, p.PortID)
		}
		if p.Role == RoleRoot {
			rp++
		}
	}
	if rp > 1 {
		t.Fatalf("root ports=%d, at most 1 allowed", rp)
	}
	logRoles(t, r, fmt.Sprintf("并发结束后不变量成立：根端口数=%d（<=1），每端口恰一种角色", rp))

	// 相同操作序列两次重放，快照逐字段一致。
	snapshot := func() string {
		x := build()
		for k := int64(0); k < 100; k++ {
			now := Time(100 + k)
			port := int64(1 + k%3)
			msg := ConfigMessage{RootID: 1, Cost: k % 7, SenderID: int64(2 + k%2), PortID: port}
			if k%5 == 4 {
				if err := x.LinkDown(now, port); err != nil {
					t.Fatal(err)
				}
			} else if err := x.Receive(now, port, msg); err != nil {
				t.Fatal(err)
			}
		}
		res, err := x.Roles(200)
		if err != nil {
			t.Fatal(err)
		}
		s := fmt.Sprintf("root=%d cost=%d", res.RootID, res.RootCost)
		for _, p := range res.Ports {
			s += fmt.Sprintf("|%d:%s:fwd=%v:msg=%v", p.PortID, p.Role, p.Forward, p.HasMessage)
		}
		return s
	}
	s1, s2 := snapshot(), snapshot()
	t.Logf("输出: 重放快照1=%s", s1)
	t.Logf("输出: 重放快照2=%s", s2)
	if s1 != s2 {
		t.Fatalf("replay differs:\n%s\n%s", s1, s2)
	}
}
