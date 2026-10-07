package cardinality

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func runtime_Gosched() { runtime.Gosched() }

// replayFromAudit 用审计日志驱动朴素串行模型重放全部 Reserve/Commit/
// Abort/Expire 事件，验证存在一种串行顺序（事件在临界区中记录的
// 全局序号顺序），使朴素模型逐步到达与并发实现完全相同的计数。
func replayFromAudit(t *testing.T, limit int64, ttl time.Duration, events []Event) *NaiveOracle {
	t.Helper()
	o := NewNaiveOracle(limit, ttl, 0)
	var lastTime int64

	for _, e := range events {
		if e.NowNanos > lastTime {
			o.advance(e.NowNanos)
			lastTime = e.NowNanos
		}
		switch e.Kind {
		case EventReserve:
			d, _ := o.Reserve(e.ReservationID, e.Decision.ExpectedV, e.NowNanos)
			if d.Accepted != e.Decision.Accepted || d.Reason != e.Decision.Reason {
				t.Fatalf("replay divergence at seq %d id=%s: replay(%v,%v) audit(%v,%v)",
					e.Seq, e.ReservationID, d.Accepted, d.Reason,
					e.Decision.Accepted, e.Decision.Reason)
			}
		case EventCommit:
			if !o.Commit(e.ReservationID, e.NowNanos) {
				t.Fatalf("replay commit lost at seq %d id=%s", e.Seq, e.ReservationID)
			}
		case EventAbort:
			if !o.Abort(e.ReservationID, e.NowNanos) {
				t.Fatalf("replay abort lost at seq %d id=%s", e.Seq, e.ReservationID)
			}
		case EventExpire:
			// 按审计事件逐个重放过期：该 ID 必须恰在此时已过租约。
			if !o.expireOneForReplay(e.ReservationID, e.NowNanos) {
				t.Fatalf("replay: expired reservation %s still active", e.ReservationID)
			}
		}

		// 每一步后，审计记录的计数必须与朴素模型完全一致——
		// 这就是"等价于某种串行顺序逐个处理"的逐步证据。
		if e.Committed != o.committed {
			t.Fatalf("seq %d committed drift: audit=%d replay=%d",
				e.Seq, e.Committed, o.committed)
		}
		if e.Inflight != len(o.active) {
			t.Fatalf("seq %d inflight drift: audit=%d replay=%d",
				e.Seq, e.Inflight, len(o.active))
		}
	}
	return o
}

// TestConcurrentRandomAgainstReplay：真实多 goroutine 并发提交
// 预留/提交/中止（墙钟下长租约，保证不发生过期），结束后用完整
// 审计日志重放给朴素串行模型，并校验最终总数恰好等于上限。
func TestConcurrentRandomAgainstReplay(t *testing.T) {
	const limit = 13
	const clients = 24
	const roundsPerClient = 30

	m := NewManager(time.Hour, NewSystemClock())
	if err := m.EnsureScope(testScope, limit, 0); err != nil {
		t.Fatal(err)
	}

	var idCounter int64
	var wg sync.WaitGroup
	wg.Add(clients)
	for c := 0; c < clients; c++ {
		c := c
		go func() {
			defer wg.Done()
			for k := 0; k < roundsPerClient; k++ {
				id := fmt.Sprintf("c%d-%d", c, atomic.AddInt64(&idCounter, 1))
				committed, _ := m.Committed(testScope)
				d, _ := m.Reserve(ReserveRequest{
					Scope: testScope, ExpectedV: committed, ReservationID: id,
				})
				if !d.Accepted {
					// 暂时性/满额拒绝：退避后继续，拒绝无副作用。
					runtime_Gosched()
					continue
				}
				// 拿到名额后约 70% 提交、30% 中止。
				if (c+k)%10 < 7 {
					m.Commit(id)
				} else {
					m.Abort(id)
				}
			}
		}()
	}
	wg.Wait()

	committed, _ := m.Committed(testScope)
	if committed > limit {
		t.Fatalf("over limit: %d > %d", committed, limit)
	}
	if st := m.Stats(); st.Inflight != 0 {
		t.Fatalf("lingering inflight: %d", st.Inflight)
	}

	// 所有名额都应被用满：每轮都有足够多的客户端在尝试。
	if committed != limit {
		t.Fatalf("expected saturation at %d, got %d", limit, committed)
	}

	// 审计重放：并发实现的每一步结果都必须在朴素串行模型的
	// 审计序号顺序下逐步重现。
	events := m.AuditLog()
	if len(events) == 0 {
		t.Fatal("audit log empty")
	}
	o := replayFromAudit(t, limit, time.Hour, events)
	if o.committed != committed {
		t.Fatalf("replay final committed %d != actual %d", o.committed, committed)
	}

	// 拒绝原因互斥性：任何 Reserve 事件要么 Accepted 且 ReasonNone，
	// 要么 !Accepted 且恰为三种原因之一。
	for _, e := range events {
		if e.Kind != EventReserve {
			continue
		}
		d := e.Decision
		switch {
		case d.Accepted && d.Reason == ReasonNone:
		case !d.Accepted && (d.Reason == ReasonBaselineConflict ||
			d.Reason == ReasonCommittedFull ||
			d.Reason == ReasonInflightOccupied):
		default:
			t.Fatalf("invalid decision: accepted=%v reason=%v", d.Accepted, d.Reason)
		}
	}
}
