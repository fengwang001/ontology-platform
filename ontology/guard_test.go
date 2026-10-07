package ontology

import (
	"sync"
	"testing"
	"time"
)

var testScope = ScopeKey{LinkType: "Employee.department", Field: "employees", ObjectID: "dept-1"}

func newTestGuard(t *testing.T, ttl time.Duration) (*Guard, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	g := NewGuard(Config{Clock: clk, LeaseTTL: ttl})
	if err := g.EnsureScope(testScope, 3); err != nil {
		t.Fatalf("ensure scope: %v", err)
	}
	return g, clk
}

func beginReq(id, src, tgt string, ver int64) BeginRequest {
	return BeginRequest{RequestID: id, Scope: testScope, SourceID: src, TargetID: tgt, ObservedVersion: ver}
}

// TestRejectPriority 基线冲突 > 已确认满额 > 进行中占用，三者互斥。
func TestRejectPriority(t *testing.T) {
	g, _ := newTestGuard(t, time.Minute)

	// 基线版本落后：即使名额充足也必须优先报基线冲突。
	d := g.Begin(beginReq("r-stale", "e-stale", "dept-1", 5))
	if d.Admitted || d.Reason != RejectBaselineConflict {
		t.Fatalf("want BASELINE_CONFLICT, got admitted=%v reason=%v", d.Admitted, d.Reason)
	}

	// 占满 3 个名额并全部提交。
	var ver int64
	for _, id := range []string{"a", "b", "c"} {
		d := g.Begin(beginReq(id, "e-"+id, "dept-1", ver))
		if !d.Admitted {
			t.Fatalf("admit %s: %v", id, d.Reason)
		}
		cr := g.Commit(id)
		if !cr.OK {
			t.Fatalf("commit %s", id)
		}
		ver = cr.Version
	}
	snap, _ := g.Snapshot(testScope)
	if snap.Version != 3 || snap.Confirmed != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}

	// 版本落后于 3：必须报基线冲突而不是满额。
	d = g.Begin(beginReq("r2", "e2", "dept-1", 2))
	if d.Reason != RejectBaselineConflict {
		t.Fatalf("want BASELINE_CONFLICT at full, got %v", d.Reason)
	}

	// 版本正确但已满：已确认满额。
	d = g.Begin(beginReq("r3", "e3", "dept-1", 3))
	if d.Reason != RejectConfirmedFull {
		t.Fatalf("want CONFIRMED_FULL, got %v", d.Reason)
	}

	// 容量从 3 放宽（约束可在无进行中请求时变更）后：
	// 已确认 2 < 上限 3，holder 预占剩余名额。
	g.mu.Lock()
	g.scopes[testScope].capacity = 4
	g.mu.Unlock()
	d = g.Begin(beginReq("holder", "e-holder", "dept-1", ver))
	if !d.Admitted {
		t.Fatalf("holder admit: %v", d.Reason)
	}

	// 版本正确、已确认未满，但被进行中请求占用：暂时性拒绝。
	d = g.Begin(beginReq("r4", "e4", "dept-1", 3))
	if d.Reason != RejectInFlight {
		t.Fatalf("want IN_FLIGHT, got %v", d.Reason)
	}
}

// TestLastSlotContention 名额恰好剩一个时的多方并发争夺：恰好一方成功。
func TestLastSlotContention(t *testing.T) {
	g, _ := newTestGuard(t, time.Minute)
	g.mu.Lock()
	g.scopes[testScope].capacity = 1
	g.mu.Unlock()

	const n = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	admit := make(chan string, n)
	rejectReasons := make(chan RejectReason, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		id := "req-" + itoa(i)
		go func() {
			defer wg.Done()
			<-start
			d := g.Begin(beginReq(id, "e-"+id, "dept-1", 0))
			if d.Admitted {
				admit <- id
			} else {
				rejectReasons <- d.Reason
			}
		}()
	}
	close(start)
	wg.Wait()
	close(admit)
	close(rejectReasons)

	var winners []string
	for id := range admit {
		winners = append(winners, id)
	}
	if len(winners) != 1 {
		t.Fatalf("want exactly 1 admitted, got %d", len(winners))
	}
	for reason := range rejectReasons {
		if reason != RejectInFlight {
			t.Fatalf("loser reason want IN_FLIGHT, got %v", reason)
		}
	}

	// 胜者提交后关联总数恰为 1，其余请求再提交必然失败（无占位泄漏）。
	if !g.Commit(winners[0]).OK {
		t.Fatalf("winner commit failed")
	}
	snap, _ := g.Snapshot(testScope)
	if snap.Confirmed != 1 || snap.InFlight != 0 || snap.Version != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}

	// 新一轮到达请求应看到已满，而不是幻影空位。
	d := g.Begin(beginReq("late", "e-late", "dept-1", 1))
	if d.Reason != RejectConfirmedFull {
		t.Fatalf("want CONFIRMED_FULL after commit, got %v", d.Reason)
	}
}

// TestFailureReleaseWindow 进行中请求失败后名额原子释放：
// 释放前到达的请求被拒；释放发生后到达的请求立刻获批，无不确定窗口。
func TestFailureReleaseWindow(t *testing.T) {
	g, _ := newTestGuard(t, time.Minute)
	g.mu.Lock()
	g.scopes[testScope].capacity = 1
	g.mu.Unlock()

	if !g.Begin(beginReq("p1", "e1", "dept-1", 0)).Admitted {
		t.Fatal("p1 should admit")
	}
	if d := g.Begin(beginReq("p2-before", "e2", "dept-1", 0)); d.Reason != RejectInFlight {
		t.Fatalf("before release want IN_FLIGHT, got %v", d.Reason)
	}

	if !g.Rollback("p1") {
		t.Fatal("rollback p1")
	}

	// 释放后第一个新请求立即感知，且严格只它一个获批。
	d := g.Begin(beginReq("p2-after", "e2", "dept-1", 0))
	if !d.Admitted {
		t.Fatalf("after release want admit, got %v", d.Reason)
	}
	d2 := g.Begin(beginReq("p3", "e3", "dept-1", 0))
	if d2.Reason != RejectInFlight {
		t.Fatalf("want IN_FLIGHT for second, got %v", d2.Reason)
	}

	snap, _ := g.Snapshot(testScope)
	if snap.Confirmed != 0 || snap.InFlight != 1 || snap.Version != 0 {
		t.Fatalf("rollback must not bump version, snapshot=%+v", snap)
	}
}

// TestLeaseExpiry 调用方中断（停心跳且超 TTL）的唯一裁定：
// 未超期不释放；超期后下一次判定原子释放，且被拒者不会被自动重试。
func TestLeaseExpiry(t *testing.T) {
	g, clk := newTestGuard(t, 10*time.Second)
	g.mu.Lock()
	g.scopes[testScope].capacity = 1
	g.mu.Unlock()

	if !g.Begin(beginReq("ghost", "eg", "dept-1", 0)).Admitted {
		t.Fatal("ghost should admit")
	}

	// TTL 内：仍占用，不能提前释放。
	clk.Advance(9 * time.Second)
	if d := g.Begin(beginReq("g2", "e2", "dept-1", 0)); d.Reason != RejectInFlight {
		t.Fatalf("before ttl want IN_FLIGHT, got %v", d.Reason)
	}

	// 心跳续期后再走 9s 仍占用。
	if !g.Heartbeat("ghost") {
		t.Fatal("heartbeat")
	}
	clk.Advance(9 * time.Second)
	if d := g.Begin(beginReq("g3", "e3", "dept-1", 0)); d.Reason != RejectInFlight {
		t.Fatalf("after heartbeat+9s want IN_FLIGHT, got %v", d.Reason)
	}

	// 超过新 deadline：下一次准入判定原子回收。
	clk.Advance(2 * time.Second)
	d := g.Begin(beginReq("g4", "e4", "dept-1", 0))
	if !d.Admitted {
		t.Fatalf("after ttl expiry want admit, got %v", d.Reason)
	}

	oc, known := g.Outcome("ghost")
	if !known || oc != OutcomeExpired {
		t.Fatalf("ghost outcome = %v known=%v, want EXPIRED", oc, known)
	}

	// 过期释放不动版本号。
	snap, _ := g.Snapshot(testScope)
	if snap.Version != 0 || snap.InFlight != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// TestRejectedLeavesNoTrace 被拒绝请求不改变关联、版本、时钟以外的任何状态，
// 且重放日志可见其判定依据。
func TestRejectedLeavesNoTrace(t *testing.T) {
	g, _ := newTestGuard(t, time.Minute)
	before, _ := g.Snapshot(testScope)

	for range 5 {
		g.Begin(beginReq("bad", "ex", "dept-1", 99)) // 基线冲突
	}
	after, _ := g.Snapshot(testScope)
	if after.Version != before.Version || after.Confirmed != before.Confirmed || after.InFlight != before.InFlight {
		t.Fatalf("reject changed state: before=%+v after=%+v", before, after)
	}

	// 同一 requestID 在被拒后可立即复用（没有占位残留）。
	d := g.Begin(beginReq("bad", "ex", "dept-1", 0))
	if !d.Admitted {
		t.Fatalf("rejected request id should be reusable, got %v", d.Reason)
	}
}

// TestDuplicateNotCounted 已存在的关联不被重复计数。
func TestDuplicateNotCounted(t *testing.T) {
	g, _ := newTestGuard(t, time.Minute)
	g.mu.Lock()
	g.scopes[testScope].capacity = 1
	g.mu.Unlock()

	if !g.Begin(beginReq("d1", "e1", "dept-1", 0)).Admitted {
		t.Fatal("d1 admit")
	}
	if !g.Commit("d1").OK {
		t.Fatal("d1 commit")
	}
	d := g.Begin(beginReq("d2", "e1", "dept-1", 1))
	if d.Reason != RejectDuplicate {
		t.Fatalf("want DUPLICATE, got %v", d.Reason)
	}
	snap, _ := g.Snapshot(testScope)
	if snap.Confirmed != 1 || snap.InFlight != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
