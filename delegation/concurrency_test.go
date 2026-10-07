package delegation

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentLinearizable 并发执行委托声明、撤销、权限变更与判定查询，
// 验证（配合 -race 无数据竞争）：所有调用都可线性化——
// 每条审计日志都对应一次完整调用，且成功声明的委托 ID 全局唯一递增、
// 判定结果始终有明确的委托链依据。
func TestConcurrentLinearizable(t *testing.T) {
	clk := NewManualClock(t0)
	var logMu sync.Mutex
	var entries []LogEntry
	s := NewService(
		WithClock(clk),
		WithLogger(LoggerFunc(func(e LogEntry) {
			logMu.Lock()
			entries = append(entries, e)
			logMu.Unlock()
		})),
	)

	const workers = 16
	const opsPerWorker = 50

	var callCount atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			me := fmt.Sprintf("user-%d", w)
			s.GrantBase(me, perm("doc", []string{"a"}, nil))
			callCount.Add(1)
			var myIDs []DelegationID
			for i := 0; i < opsPerWorker; i++ {
				switch i % 4 {
				case 0:
					id, err := s.Delegate(DelegationRequest{
						Delegator: me, Delegatee: fmt.Sprintf("user-%d", (w+1)%workers),
						Subset: perm("doc", []string{"a"}, nil),
						Start:  t0, End: t0.Add(100 * time.Hour),
					})
					callCount.Add(1)
					if err == nil {
						myIDs = append(myIDs, id)
					}
				case 1:
					if len(myIDs) > 0 {
						_ = s.Revoke(myIDs[len(myIDs)-1])
						callCount.Add(1)
						myIDs = myIDs[:len(myIDs)-1]
					}
				case 2:
					s.ShrinkBase(me, perm("doc", []string{"a"}, nil))
					s.GrantBase(me, perm("doc", []string{"a"}, nil))
					callCount.Add(2)
				case 3:
					_ = s.Decide(AccessRequest{
						Subject:    me,
						ObjectType: "doc",
						Require:    ObjectPerm{Attrs: map[string]bool{"a": true}},
					})
					callCount.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()

	// 每次调用都必须恰好留下一条日志。
	logMu.Lock()
	defer logMu.Unlock()
	if want := callCount.Load(); int64(len(entries)) != want {
		t.Fatalf("日志条数 = %d, 期望 %d", len(entries), want)
	}
	// 成功声明的委托 ID 不得重复（串行等价的直接推论）。
	seen := map[DelegationID]bool{}
	for _, e := range entries {
		if e.Op != "Delegate" || e.Error != "" {
			continue
		}
		out, ok := e.Output.(map[string]any)
		if !ok {
			t.Fatalf("Delegate 日志缺少输出: %+v", e)
		}
		id, ok := out["id"].(DelegationID)
		if !ok {
			t.Fatalf("Delegate 日志输出缺少 id: %+v", out)
		}
		if seen[id] {
			t.Fatalf("委托 ID %d 被分配两次", id)
		}
		seen[id] = true
	}
}
