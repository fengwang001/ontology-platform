package cardinality

import (
	"testing"
	"time"
)

var testScope = ScopeKey{LinkType: "lt", Source: "a", Target: "b"}

func newTestManager(t *testing.T, limit Limit) (*Manager, *FakeClock) {
	t.Helper()
	clk := NewFakeClock(1_000_000_000)
	m := NewManager(time.Hour, clk)
	if err := m.EnsureScope(testScope, limit, 0); err != nil {
		t.Fatalf("EnsureScope: %v", err)
	}
	return m, clk
}

func reserve(t *testing.T, m *Manager, id string, expectedV int64) Decision {
	t.Helper()
	d, _ := m.Reserve(ReserveRequest{Scope: testScope, ExpectedV: expectedV, ReservationID: id})
	return d
}

// 基本 happy path：预留不推进版本，提交后版本 +1、已确认数 +1。
func TestReserveCommitHappyPath(t *testing.T) {
	m, _ := newTestManager(t, 2)

	d := reserve(t, m, "r1", 0)
	if !d.Accepted || d.Reason != ReasonNone {
		t.Fatalf("want accepted, got %+v", d)
	}
	if d.Snapshot.Remaining != 1 {
		t.Fatalf("want remaining 1, got %d", d.Snapshot.Remaining)
	}
	if got, _ := m.Committed(testScope); got != 0 {
		t.Fatalf("reserve must not advance committed count, got %d", got)
	}
	if !m.Commit("r1") {
		t.Fatal("commit r1 failed")
	}
	if got, _ := m.Committed(testScope); got != 1 {
		t.Fatalf("want committed 1, got %d", got)
	}
	// 提交是终结性的：重复 Commit/Abort 均幂等无副作用。
	if m.Commit("r1") || m.Abort("r1") {
		t.Fatal("finalized reservation must not be reusable")
	}
	if got, _ := m.Committed(testScope); got != 1 {
		t.Fatalf("idempotent replay changed state: %d", got)
	}
}

// 判定顺序：基线冲突优先于一切名额判定。
// 构造 committed=1、limit=1（已确认满）且基线落后：必须报基线冲突。
func TestDecisionOrderBaselineFirst(t *testing.T) {
	clk := NewFakeClock(0)
	m := NewManager(time.Hour, clk)
	if err := m.EnsureScope(testScope, 1, 1); err != nil {
		t.Fatal(err)
	}
	d := reserve(t, m, "r1", 0) // 实际版本是 1
	if d.Reason != ReasonBaselineConflict {
		t.Fatalf("want baseline conflict, got %v", d.Reason)
	}
	if d.ObservedV != 1 {
		t.Fatalf("want observed version 1, got %d", d.ObservedV)
	}
}

// 判定顺序：已确认达上限优先于进行中占用。
func TestDecisionOrderCommittedBeforeInflight(t *testing.T) {
	m, _ := newTestManager(t, 1)
	d1 := reserve(t, m, "r1", 0)
	if !d1.Accepted {
		t.Fatalf("r1: %+v", d1)
	}
	if !m.Commit("r1") {
		t.Fatal("commit")
	}
	// 现在 committed=1=limit。再请求：即使没有任何进行中预留，
	// 也必须报"已确认满"而非"进行中占用"。
	d2 := reserve(t, m, "r2", 1)
	if d2.Reason != ReasonCommittedFull {
		t.Fatalf("want committed full, got %v", d2.Reason)
	}
}

// 进行中预留占用名额：committed 未满时得到暂时性拒绝。
func TestInflightOccupiesSlot(t *testing.T) {
	m, _ := newTestManager(t, 1)
	d1 := reserve(t, m, "r1", 0)
	if !d1.Accepted {
		t.Fatalf("r1: %+v", d1)
	}
	d2 := reserve(t, m, "r2", 0)
	if d2.Accepted || d2.Reason != ReasonInflightOccupied {
		t.Fatalf("want inflight-occupied, got %+v", d2)
	}
	if d2.Snapshot.Inflight != 1 || d2.Snapshot.Remaining != 0 {
		t.Fatalf("bad evidence: %+v", d2.Snapshot)
	}
}

// Abort 原子释放名额：被拒请求不会自动重试，但新请求立刻可见名额。
func TestAbortReleasesImmediately(t *testing.T) {
	m, _ := newTestManager(t, 1)
	if !reserve(t, m, "r1", 0).Accepted {
		t.Fatal("r1")
	}
	if reserve(t, m, "r2", 0).Reason != ReasonInflightOccupied {
		t.Fatal("r2 should be blocked")
	}
	if !m.Abort("r1") {
		t.Fatal("abort")
	}
	// 新到达的请求立刻感知释放；此前的 r2 不会被自动唤醒。
	d3 := reserve(t, m, "r3", 0)
	if !d3.Accepted {
		t.Fatalf("slot must be visible immediately after abort: %+v", d3)
	}
	if st := m.Stats(); st.Inflight != 1 {
		t.Fatalf("want exactly 1 inflight, got %+v", st)
	}
}

// 被拒绝的请求与"从未发生"不可区分：关联数、版本、进行中集合不变。
func TestRejectionHasNoSideEffects(t *testing.T) {
	m, _ := newTestManager(t, 1)
	if !reserve(t, m, "r1", 0).Accepted {
		t.Fatal("r1")
	}
	before := m.Stats()
	beforeCommitted, _ := m.Committed(testScope)

	for i := 0; i < 5; i++ {
		d := reserve(t, m, "rejected", 0)
		if d.Reason != ReasonInflightOccupied {
			t.Fatalf("iter %d: %v", i, d.Reason)
		}
	}
	// 基线冲突类拒绝同样零副作用。
	for i := 0; i < 5; i++ {
		if reserve(t, m, "rejected-bc", 99).Reason != ReasonBaselineConflict {
			t.Fatal("want baseline conflict")
		}
	}

	after := m.Stats()
	afterCommitted, _ := m.Committed(testScope)
	// 审计记录数增加（证据要求），但约束状态（作用域数/进行中数）不变。
	if after.Scopes != before.Scopes || after.Inflight != before.Inflight {
		t.Fatalf("constraint state changed: before=%+v after=%+v", before, after)
	}
	if afterCommitted != beforeCommitted {
		t.Fatalf("committed changed: %d -> %d", beforeCommitted, afterCommitted)
	}
	// 被拒的 ID 从未进入存活索引：用它提交/中止都必须是 no-op。
	if m.Commit("rejected") || m.Abort("rejected") {
		t.Fatal("rejected reservation id must never exist")
	}
}

// 租约：未过期的预留持续占位；时钟越过截止时刻后被原子回收，
// 这是调用方中断情形唯一且明确的裁定依据。
func TestLeaseExpiryReclaimsInterruptedRequest(t *testing.T) {
	clk := NewFakeClock(0)
	m := NewManager(10*time.Second, clk)
	if err := m.EnsureScope(testScope, 1, 0); err != nil {
		t.Fatal(err)
	}

	d, deadline := m.Reserve(ReserveRequest{Scope: testScope, ExpectedV: 0, ReservationID: "r1"})
	if !d.Accepted || deadline != 10_000_000_000 {
		t.Fatalf("bad reserve: %+v deadline=%d", d, deadline)
	}

	// 截止时刻之前一刻：名额仍被占用，不能提前释放。
	clk.Set(9_999_999_999)
	if got := reserve(t, m, "r2", 0); got.Reason != ReasonInflightOccupied {
		t.Fatalf("slot must stay occupied before deadline, got %v", got.Reason)
	}
	// 心跳可以续租。
	if !m.Heartbeat("r1", 5*time.Second) {
		t.Fatal("heartbeat")
	}
	// 续租后截止时刻 = now(9.999999999s) + 5s = 14.999999999s；
	// 该时刻之前仍占位，严格等于即算过期（deadline <= now）。
	clk.Set(14_999_999_998)
	if got := reserve(t, m, "r2", 0); got.Reason != ReasonInflightOccupied {
		t.Fatalf("heartbeat must extend occupancy, got %v", got.Reason)
	}

	// 越过新的截止时刻：调用方被裁定为中断，名额立即释放。
	clk.Set(14_999_999_999)
	reaped := m.Sweep()
	if len(reaped) != 1 || reaped[0] != "r1" {
		t.Fatalf("want r1 reaped, got %v", reaped)
	}
	d2 := reserve(t, m, "r2", 0)
	if !d2.Accepted {
		t.Fatalf("slot must be free after expiry: %+v", d2)
	}

	// 过期预留不能复活：对 r1 的 Commit/Abort/Heartbeat 均无副作用。
	if m.Commit("r1") || m.Abort("r1") || m.Heartbeat("r1", time.Hour) {
		t.Fatal("expired reservation must not be revivable")
	}
	if got, _ := m.Committed(testScope); got != 0 {
		t.Fatalf("expiry must not create links, got %d", got)
	}
}

// 惰性回收：不显式 Sweep，新 Reserve 到达时也立即感知过期释放，
// 不存在释放后名额仍不确定的时间窗口。
func TestLazyReclaimOnReserve(t *testing.T) {
	clk := NewFakeClock(0)
	m := NewManager(time.Second, clk)
	if err := m.EnsureScope(testScope, 1, 0); err != nil {
		t.Fatal(err)
	}
	if !reserve(t, m, "r1", 0).Accepted {
		t.Fatal("r1")
	}
	clk.Advance(time.Second + time.Nanosecond)
	d := reserve(t, m, "r2", 0)
	if !d.Accepted {
		t.Fatalf("new arrival must observe release immediately: %+v", d)
	}
}

// 审计证据：拒绝也有记录，序号严格单调，可还原每次判定依据。
func TestAuditEvidence(t *testing.T) {
	m, _ := newTestManager(t, 1)
	reserve(t, m, "r1", 0)
	reserve(t, m, "r2", 0) // 占用拒绝
	m.Abort("r1")

	events := m.AuditLog()
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	for i, e := range events {
		if e.Seq != int64(i) {
			t.Fatalf("event %d seq=%d", i, e.Seq)
		}
	}
	if events[0].Kind != EventReserve || events[1].Kind != EventReserve ||
		events[2].Kind != EventAbort {
		t.Fatalf("unexpected kinds: %v", []EventKind{events[0].Kind, events[1].Kind, events[2].Kind})
	}
	if events[1].Decision == nil || events[1].Decision.Reason != ReasonInflightOccupied {
		t.Fatal("rejection decision must be fully recorded")
	}
	if events[1].Inflight != 1 || events[2].Inflight != 0 {
		t.Fatalf("event inflight counts wrong: %+v %+v", events[1], events[2])
	}

	// AuditLog 返回副本，外部修改不得影响管理器。
	events[0].Kind = EventCommit
	if m.AuditLog()[0].Kind != EventReserve {
		t.Fatal("audit log must be defensive copy")
	}
}

// 非法参数。
func TestInvalidInputs(t *testing.T) {
	m, _ := newTestManager(t, 1)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("empty reservation id must panic")
			}
		}()
		m.Reserve(ReserveRequest{Scope: testScope, ExpectedV: 0})
	}()
	if err := m.EnsureScope(testScope, -1, 0); err != ErrInvalidLimit {
		t.Fatalf("want ErrInvalidLimit, got %v", err)
	}
	if err := m.EnsureScope(testScope, 1, 2); err != ErrInvalidLimit {
		t.Fatalf("limit below committed, got %v", err)
	}
	if err := m.EnsureScope(testScope, 1, -1); err != ErrInvalidCommitted {
		t.Fatalf("got %v", err)
	}
}

// 未知作用域：基线冲突且不隐式创建（零副作用）。
func TestUnknownScopeIsBaselineConflict(t *testing.T) {
	clk := NewFakeClock(0)
	m := NewManager(time.Hour, clk)
	d, deadline := m.Reserve(ReserveRequest{
		Scope:         ScopeKey{LinkType: "ghost"},
		ExpectedV:     0,
		ReservationID: "x",
	})
	if d.Accepted || d.Reason != ReasonBaselineConflict || deadline != 0 {
		t.Fatalf("got %+v deadline=%d", d, deadline)
	}
	if st := m.Stats(); st.Scopes != 0 || st.Inflight != 0 {
		t.Fatalf("rejection created state: %+v", st)
	}
}

// 终结路径的各种幂等/过期边缘分支。
func TestFinalizationEdgePaths(t *testing.T) {
	clk := NewFakeClock(0)
	m := NewManager(2*time.Second, clk)
	if err := m.EnsureScope(testScope, 2, 0); err != nil {
		t.Fatal(err)
	}

	// 未知 ID：Commit/Abort/Heartbeat 均为 no-op。
	if m.Commit("ghost") || m.Abort("ghost") || m.Heartbeat("ghost", time.Second) {
		t.Fatal("unknown id must be no-op")
	}

	if !reserve(t, m, "r1", 0).Accepted {
		t.Fatal("r1")
	}
	if !reserve(t, m, "r2", 0).Accepted {
		t.Fatal("r2")
	}

	// r1 显式长续租到 10s，跨过本轮所有检查点。
	if !m.Heartbeat("r1", 10*time.Second) {
		t.Fatal("heartbeat r1")
	}

	clk.Set(3_000_000_000)
	// r2 默认租约 2s，此刻已过期：对它 Commit 必须返回 false 且
	// 按"中断"回收，绝不产生关联。
	if m.Commit("r2") {
		t.Fatal("commit on expired reservation must fail")
	}
	if got, _ := m.Committed(testScope); got != 0 {
		t.Fatalf("expired commit created link: %d", got)
	}

	// r1 长续租后仍存活；Abort 成功释放。
	if !m.Abort("r1") {
		t.Fatal("r1 still alive and abortable")
	}

	// 另一个预留过期后走 Abort 路径（已在 Commit 分支回收过 r2，
	// 这里新建 r3 覆盖 Abort 的过期分支）。
	clk.Set(4_000_000_000)
	if !reserve(t, m, "r3", 0).Accepted {
		t.Fatal("r3")
	}
	// 非正 TTL 回落到管理器默认 TTL（2s）：r3 截止于 6s。
	if !m.Heartbeat("r3", 0) {
		t.Fatal("default-ttl heartbeat")
	}
	clk.Set(4_000_000_001 + 2_000_000_000)
	if m.Abort("r3") {
		t.Fatal("abort on expired reservation must fail")
	}
	if got, _ := m.Committed(testScope); got != 0 {
		t.Fatalf("no links should exist, got %d", got)
	}
	if st := m.Stats(); st.Inflight != 0 {
		t.Fatalf("all reservations reaped, inflight=%d", st.Inflight)
	}

	// 默认 clock=nil 时使用系统单调时钟，基本可用。
	m2 := NewManager(time.Minute, nil)
	if err := m2.EnsureScope(testScope, 1, 0); err != nil {
		t.Fatal(err)
	}
	d, _ := m2.Reserve(ReserveRequest{Scope: testScope, ExpectedV: 0, ReservationID: "s"})
	if !d.Accepted || d.NowNanos < 0 {
		t.Fatalf("system clock reserve broken: %+v", d)
	}

	// 非正 TTL 在构造时 panic。
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("non-positive ttl must panic")
			}
		}()
		NewManager(0, clk)
	}()
}
