package orphanreclaim

import "testing"

// 第一代宽限临界：deadline 恰好到期即推进；刚差一刻不推进。
func TestGen1Boundary_ExactlyAndJustBefore(t *testing.T) {
	r, c, _ := newTestEngine(t, testConfig(), 0)
	r.CreateObject("a")
	r.CreateObject("b")
	r.CreateObject("s")

	// t=0 加入联合单边 -> 孤儿，gen1，deadline=10。
	mustAdd(t, r, "s", "a", "J1") // clocks: now() -> 0
	c.t = 1
	mustAdd(t, r, "s", "b", "J1") // now() -> 1, deadline=11

	// 时刻 10：a 恰好到期（<=）应升入 gen2；b 还差一刻，留在 gen1。
	c.t = 10
	r.Advance()
	assertGen(t, r, "a", Gen2)
	assertGen(t, r, "b", Gen1)

	// 时刻 11：b 恰好到期。
	c.t = 11
	r.Advance()
	assertGen(t, r, "b", Gen2)
}

// 第二代宽限临界：gen2 deadline 恰好到期才清理；gen2 更短(4)。
func TestGen2Boundary_PurgeExactly(t *testing.T) {
	r, c, _ := newTestEngine(t, testConfig(), 0)
	r.CreateObject("a")
	r.CreateObject("s")
	mustAdd(t, r, "s", "a", "J1") // t=0 入 gen1, deadline 10

	c.t = 10
	r.Advance() // -> gen2, since=10, deadline=14
	assertGen(t, r, "a", Gen2)

	c.t = 13 // 刚差一刻：不得清理
	r.Advance()
	assertGen(t, r, "a", Gen2)

	c.t = 14 // 恰好到期：真正清理
	r.Advance()
	assertGone(t, r, "a")
}

// 第一代内重新获得入边：脱离队列、清空记录；再次变孤儿重新从第一代起算，
// 不得延续此前已经过去的宽限时长。
func TestGen1RescueThenReorphan_RestartsGen1(t *testing.T) {
	r, c, _ := newTestEngine(t, testConfig(), 100)
	r.CreateObject("a")
	r.CreateObject("s1")
	r.CreateObject("s2")

	mustAdd(t, r, "s1", "a", "J1") // t=100 入 gen1, deadline 110
	assertGen(t, r, "a", Gen1)

	c.t = 109                     // 宽限期将满
	mustAdd(t, r, "s2", "a", "I") // 独立保留：救回
	assertGen(t, r, "a", GenNone)

	c.t = 500
	mustRemove(t, r, "s2", "a", "I") // 再次变孤儿：必须从第一代重新起算
	assertGen(t, r, "a", Gen1)
	snap := r.Snapshot()
	if snap.Since["a"] != 500 || snap.Deadline["a"] != 510 {
		t.Fatalf("fresh gen1 timer expected since=500 deadline=510, got %d/%d",
			snap.Since["a"], snap.Deadline["a"])
	}

	c.t = 509 // 按旧计时此刻早该被清理/晋升；新计时下仍在第一代
	r.Advance()
	assertGen(t, r, "a", Gen1)
}

// 第二代内重新获得入边：直接退回非孤儿，不回第一代、不留代际记忆。
func TestGen2Rescue_NoGenerationalMemory(t *testing.T) {
	r, c, _ := newTestEngine(t, testConfig(), 0)
	r.CreateObject("a")
	r.CreateObject("s1")
	r.CreateObject("s2")
	mustAdd(t, r, "s1", "a", "J1")
	c.t = 10
	r.Advance() // gen2, deadline 14
	assertGen(t, r, "a", Gen2)

	c.t = 13
	mustAdd(t, r, "s2", "a", "I") // 二代内救回
	assertGen(t, r, "a", GenNone)

	// 立刻再次变孤儿：必须从第一代全新起算（无代际记忆）。
	c.t = 13
	mustRemove(t, r, "s2", "a", "I")
	assertGen(t, r, "a", Gen1)
	snap := r.Snapshot()
	if snap.Since["a"] != 13 || snap.Deadline["a"] != 23 {
		t.Fatalf("gen2 rescue must forget generation, got since=%d deadline=%d",
			snap.Since["a"], snap.Deadline["a"])
	}
}

// 到期扫描的瞬间重评：deadline 到达但对象在扫描前已被重新引用，
// 不得发生「判定已过期但队列记录尚未更新」的清理。
func TestAdvanceRecheckAtDeadline(t *testing.T) {
	r, c, lg := newTestEngine(t, testConfig(), 0)
	r.CreateObject("a")
	r.CreateObject("s1")
	r.CreateObject("s2")
	mustAdd(t, r, "s1", "a", "J1") // gen1 deadline 10
	c.t = 10
	mustAdd(t, r, "s2", "a", "I") // 晋升前一刻救回
	r.Advance()
	assertGen(t, r, "a", GenNone)
	found := false
	for _, d := range lg.decisions {
		if d.Object == "a" && d.Trigger == "add_link" && d.Outcome == "rescued" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected rescue decision to be logged")
	}
}

// 同一次扫描不得让对象同时属于两代：deadline 同刻且第二代更短时，
// 新晋升对象不参与当次第二代清理。
func TestNoSimultaneousGenerations(t *testing.T) {
	cfg := testConfig()
	cfg.GraceGen1 = 5
	cfg.GraceGen2 = 5 // 同长，制造「同一 now 下两代都到期」的压力
	r, c, _ := newTestEngine(t, cfg, 0)
	r.CreateObject("a")
	r.CreateObject("s")
	mustAdd(t, r, "s", "a", "J1") // t=0 gen1 deadline 5
	c.t = 5
	r.Advance() // 仅晋升 gen2，不得在同一扫描里清理
	assertGen(t, r, "a", Gen2)
	c.t = 10
	r.Advance()
	assertGone(t, r, "a")
}

// 第二代对象到期出堆时锁内重评发现已满足保留：按 advance_recheck 救回，
// 不得清理、不得回第一代。
func TestGen2AdvanceRecheckRescue(t *testing.T) {
	r, c, lg := newTestEngine(t, testConfig(), 0)
	r.CreateObject("a")
	r.CreateObject("s")
	mustAdd(t, r, "s", "a", "J1") // gen1 deadline 10
	c.t = 10
	r.Advance() // gen2 deadline 14
	assertGen(t, r, "a", Gen2)

	c.t = 14
	// 不走公开 AddLink（那会在同一全序点立即救回），直接铺设入边，
	// 让 Advance 出堆后的重评首次观察到联合组已满足。
	injectEdgeNoReassess(r, "s", "a", "J2")
	r.Advance()
	assertGen(t, r, "a", GenNone)

	found := false
	for _, d := range lg.decisions {
		if d.Object == "a" && d.Trigger == "advance_recheck" &&
			d.PreviousGen == Gen2 && !d.Orphan &&
			d.Outcome == "rescued" && d.Basis.Layer == "joint" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected gen2 advance_recheck rescue logged with joint basis")
	}
}
