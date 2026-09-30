package stp

import (
	"testing"
	"time"
)

// TestThreeBridgeRing verifies the headline convergence property: three
// bridges connected in a ring end up with exactly one blocking port.
//
// Topology (each link cost 1):
//
//	b1 -------- b2
//	| p2     p1 |
//	| p1     p2 |
//	b3 ----------
//
// Links: (b1.p1,b2.p1), (b2.p2,b3.p1), (b3.p2,b1.p2).
func TestThreeBridgeRing(t *testing.T) {
	const age = 30 * time.Second
	b1, _ := NewBridge(1, []int{1, 2}, []int{1, 1}, age)
	b2, _ := NewBridge(2, []int{1, 2}, []int{1, 1}, age)
	b3, _ := NewBridge(3, []int{1, 2}, []int{1, 1}, age)
	bridges := map[int]*Bridge{1: b1, 2: b2, 3: b3}

	type endpoint struct{ bridge, port int }
	links := [][2]endpoint{
		{{1, 1}, {2, 1}},
		{{2, 2}, {3, 1}},
		{{3, 2}, {1, 2}},
	}

	// Each iteration: compute roles, advertise on designated ports, then store
	// one received message per link (last advertiser wins, matching the
	// "newest message replaces old" rule).
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for round := 0; round < 10; round++ {
		now := t0.Add(time.Duration(round) * time.Second)
		adverts := map[endpoint]ConfigMessage{}
		for id, br := range bridges {
			rootID, cost, roles := br.Roles(now)
			for _, s := range roles {
				if s.Role == RoleDesignated {
					adverts[endpoint{id, s.Port}] = ConfigMessage{rootID, cost, id, s.Port}
				}
			}
		}
		for _, link := range links {
			for side := 0; side < 2; side++ {
				src, dst := link[side], link[1-side]
				if msg, ok := adverts[src]; ok {
					if err := bridges[dst.bridge].Receive(now, dst.port, msg); err != nil {
						t.Fatalf("round=%d 链路消息被拒: %v", round, err)
					}
				}
			}
		}
	}

	converged := t0.Add(10 * time.Second)
	totalBlocking := 0
	rootPorts := map[int]int{}
	for id, br := range bridges {
		rootID, cost, roles := br.Roles(converged)
		t.Logf("收敛查询 bridge=%d -> 输出: 根桥=%d 到根开销=%d", id, rootID, cost)
		blocking, designated := 0, 0
		for _, s := range roles {
			t.Logf("  判定依据: bridge=%d port=%d 角色=%s", id, s.Port, s.Role)
			switch s.Role {
			case RoleBlocking:
				blocking++
			case RoleRoot:
				rootPorts[id] = s.Port
			case RoleDesignated:
				designated++
			}
		}
		if id == 1 {
			if rootID != 1 || cost != 0 {
				t.Fatalf("b1 编号最小应为根桥, 实际 root=%d cost=%d", rootID, cost)
			}
			if blocking != 0 || designated != 2 {
				t.Fatalf("根桥端口应全部指定, 实际 指定=%d 阻塞=%d", designated, blocking)
			}
		} else {
			if rootID != 1 {
				t.Fatalf("bridge=%d 应认定 b1 为根桥, 实际 %d", id, rootID)
			}
			if _, hasRoot := rootPorts[id]; !hasRoot {
				t.Fatalf("非根桥 %d 必须恰有一个根端口", id)
			}
		}
		totalBlocking += blocking
	}
	if totalBlocking != 1 {
		t.Fatalf("三环成环收敛后应恰有一个阻塞端口, 实际 %d", totalBlocking)
	}
	if len(rootPorts) != 2 {
		t.Fatalf("非根桥数量为 2，根端口总数应为 2, 实际 %d", len(rootPorts))
	}
	t.Logf("收敛结论: 根桥=b1(无根端口)，b2/b3 各一个根端口，全环恰好 1 个阻塞端口")
}
