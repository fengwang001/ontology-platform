package lifecycle

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 并发转换与并发查询交织：
//  1. 查询永远观察到合法状态，且对象视图与其出边视图对同一身份一致；
//  2. 转换结果可线性化：宽限内至多一次撤销生效，冻结到期后恰好归档一次。
func TestConcurrentTransitionsAndQueries(t *testing.T) {
	clock := NewFakeClock(t0)
	log := NewSliceLogger()
	audit := NewMemoryAuditLog()
	svc := NewService(clock, audit, log)
	mustCreate(t, svc, nil, "o")
	mustCreate(t, svc, nil, "t")
	mustCreate(t, svc, nil, "p")
	if err := svc.AddEdge(Edge{ID: "e", SourceID: "o", TargetID: "t"}); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var readers sync.WaitGroup
	var wg sync.WaitGroup
	var readerErrMu sync.Mutex
	var readerErr error
	setErr := func(format string, args ...any) {
		readerErrMu.Lock()
		if readerErr == nil {
			readerErr = fmt.Errorf(format, args...)
		}
		readerErrMu.Unlock()
	}
	validStates := map[State]bool{
		StateAlive: true, StateGrace: true, StateFrozen: true, StateArchived: true,
	}

	// 并发查询：同一身份下对象可见性与出边可见性必须一致。
	for range 6 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				for _, actor := range []Identity{IdentityUser, IdentityAdmin} {
					v, edges := svc.ViewWithEdges("o", actor)
					if !validStates[v.State] {
						setErr("observed illegal state %q", v.State)
						return
					}
					objectVisible := v.Visible
					if v.Visible && len(edges) != 1 {
						setErr("inconsistent visibility: object visible=%v edges=%d",
							v.Visible, len(edges))
						return
					}
					if !objectVisible && len(edges) != 0 {
						setErr("inconsistent visibility: object hidden but edges=%d", len(edges))
						return
					}
					if actor == IdentityUser && v.Visible && v.State != StateAlive {
						setErr("user saw non-alive object: %s", v.State)
						return
					}
				}
			}
		}()
	}

	// 并发转换：多个撤销者抢同一宽限窗口，至多一个成功。
	clock.Set(t0)
	if err := svc.Delete("o", IdentityAdmin, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var undoOK, undoRejected atomic.Int64
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := svc.Undo("o", IdentityAdmin)
			if err == nil {
				undoOK.Add(1)
			} else {
				undoRejected.Add(1)
			}
		}()
	}
	// 并发非法方向请求。
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 干扰请求打到独立对象，保证对 o 的唯一合法转换是撤销。
			_ = svc.Freeze("p", IdentityAdmin, time.Hour)
			_ = svc.Delete("p", IdentityAdmin, t0.Add(2*time.Hour))
			_ = svc.GetView("p", IdentityUser)
		}()
	}

	// 等转换 goroutine 结束后停止查询。
	wg.Wait()
	stop.Store(true)
	readers.Wait()

	if got := undoOK.Load(); got != 1 {
		t.Fatalf("successful undos = %d, want exactly 1 (rejected=%d)",
			got, undoRejected.Load())
	}
	if got := svc.GetView("o", IdentityAdmin); got.State != StateAlive {
		t.Fatalf("state after concurrent undo = %s, want alive", got.State)
	}
	if readerErr != nil {
		t.Fatal(readerErr)
	}
}

// 冻结到期时并发归档与查询：最终恰好归档一次，审计中归档事件只有一条。
func TestConcurrentFreezeExpirySettlesOnce(t *testing.T) {
	clock := NewFakeClock(t0)
	memAudit := NewMemoryAuditLog()
	svc := NewService(clock, NewCountingAuditLog(memAudit), NewSliceLogger())
	mustCreate(t, svc, nil, "o")
	if err := svc.Freeze("o", IdentityAdmin, time.Minute); err != nil {
		t.Fatal(err)
	}

	clock.Set(t0.Add(time.Minute))
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.Archive("o", IdentityAdmin)
			_ = svc.GetView("o", IdentityAdmin)
		}()
	}
	wg.Wait()

	var archives int
	for _, tr := range memAudit.All() {
		if tr.To == StateArchived {
			archives++
		}
	}
	if archives != 1 {
		t.Fatalf("archive transitions = %d, want exactly 1", archives)
	}
	if got := svc.GetView("o", IdentityAdmin); got.State != StateArchived {
		t.Fatalf("state = %s", got.State)
	}
}

// go test -race 下的纯压力交织（与上面逻辑断言叠加，由 -race 报告数据竞争）。
func TestConcurrentHighContention(t *testing.T) {
	clock := NewFakeClock(t0)
	svc := NewService(clock, NewMemoryAuditLog(), NewSliceLogger())
	const n = 20
	for i := range n {
		if err := svc.CreateObject(fmt.Sprintf("o%d", i), Attrs{"i": fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("o%d", i)
			for range 100 {
				_ = svc.Freeze(id, IdentityAdmin, time.Millisecond)
				_ = svc.Archive(id, IdentityAdmin)
				_ = svc.GetView(id, IdentityAdmin)
				_ = svc.GetView(id, IdentityUser)
			}
		}(i)
	}
	wg.Wait()

	clock.Set(t0.Add(time.Hour))
	for i := range n {
		id := fmt.Sprintf("o%d", i)
		v := svc.GetView(id, IdentityAdmin)
		if v.State != StateArchived {
			t.Fatalf("%s final state = %s, want archived", id, v.State)
		}
	}
}
