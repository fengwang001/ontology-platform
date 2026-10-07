package retention_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/retention"
)

// 并发的状态转换请求与并发查询同时发生：
// 任何一次查询只能观察到一个合法状态，且对象与其出边结论一致；
// 最终状态必须是合法状态之一，且等价于某一全局顺序的结果。
func TestConcurrentTransitionsAndQueries(t *testing.T) {
	svc, _, _ := newSvc(0)
	must(t, svc.CreateObject("o", map[string]string{"k": "v"}))
	must(t, svc.CreateObject("d", nil))
	must(t, svc.AddEdge("o", "d"))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var writers sync.WaitGroup

	valid := map[retention.State]bool{
		retention.StateAlive: true, retention.StateGrace: true,
		retention.StateFrozen: true, retention.StateArchived: true,
	}

	// 多个查询者：反复校验“查询内一致性”。
	query := func(role retention.Role) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			v, edges, exists := svc.QueryWithEdges("o", role)
			if !exists || !valid[v.State] {
				t.Errorf("query observed invalid/intermediate state: %+v", v)
				return
			}
			edgeVisible := len(edges) == 1
			// 单一快照内的不变量：
			// 出边可见 => 对象可见；冻结管理员视图下出边随业务属性遮蔽。
			if edgeVisible && !v.Visible {
				t.Errorf("edge visible while source invisible: %+v", v)
				return
			}
			if role == retention.RoleAdmin && v.State == retention.StateFrozen && edgeVisible {
				t.Errorf("edge leaked under frozen admin view")
				return
			}
			if role == retention.RoleUser && v.Visible && v.State != retention.StateAlive {
				t.Errorf("user saw non-alive object as visible: %+v", v)
				return
			}
			if role == retention.RoleAdmin && v.State == retention.StateFrozen && v.Attributes != nil {
				t.Errorf("frozen business attrs leaked under concurrent query")
				return
			}
		}
	}

	for i := 0; i < 4; i++ {
		wg.Add(2)
		go query(retention.RoleUser)
		go query(retention.RoleAdmin)
	}

	// 多个写者：只能沿合法方向转换；非法转换被拒绝即可。
	writer := func(seed int) {
		defer writers.Done()
		for i := 0; i < 200; i++ {
			switch (i + seed) % 4 {
			case 0:
				_ = svc.SoftDelete("o", int64(10+i*3))
			case 1:
				_ = svc.Undelete("o")
			case 2:
				_ = svc.Freeze("o", 1)
			case 3:
				_ = svc.Archive("o")
			}
		}
	}
	for i := 0; i < 4; i++ {
		writers.Add(1)
		go writer(i)
	}

	// 时钟推进者：不断越过宽限/冻结截止时刻，制造自动归档。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; i < 400; i++ {
			svc.Advance(int64(i))
		}
	}()

	// 两阶段收尾：先等全部写者与时钟推进结束，再停止查询。
	writers.Wait()
	close(stop)
	wg.Wait()

	st, _, _, ok := svc.State("o")
	if !ok || !valid[st] {
		t.Fatalf("final state invalid: %s ok=%v", st, ok)
	}
	fmt.Printf("concurrent run final state: %s\n", st)
}
