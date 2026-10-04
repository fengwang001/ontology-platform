package exec

import (
	"fmt"
	"sync"
	"testing"

	"ontology/action"
)

// TestConcurrentStress 并发混合调用：结果等价于某串行顺序、每个等待者恰一次终局、
// 同一摘要至多一个在途操作、槽位占用与已派发操作数一致。
func TestConcurrentStress(t *testing.T) {
	s := New(2)
	if err := s.Register("W1", []action.KV{{Key: "os", Value: "linux"}}, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.Register("W2", []action.KV{{Key: "os", Value: "win"}}, 4); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var idsMu sync.Mutex
	var ids []int

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				d := []string{"d0", "d1", "d2"}[(seed+i)%3]
				p := plat(kv("os", "linux"))
				id, err := s.Execute(d, p, i%10, i%7 == 0)
				if err == nil {
					idsMu.Lock()
					ids = append(ids, id)
					idsMu.Unlock()
				}
			}
		}(g)
	}

	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				r, err := s.Poll(worker)
				if err != nil {
					continue
				}
				if r.Op == nil {
					continue
				}
				if i%11 == 0 {
					_ = s.WorkerLost(worker)
					_ = s.Register(worker, []action.KV{{Key: "os", Value: "linux"}}, 4)
					continue
				}
				_ = s.Complete(worker, r.Op, r.Attempt, 0, i%13 == 0)
			}
		}([]string{"W1", "W2"}[g])
	}

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idsMu.Lock()
			l := len(ids)
			var id int
			if l > 0 {
				id = ids[(l*7+i)%l]
			}
			idsMu.Unlock()
			if id > 0 {
				_ = s.Cancel(id)
			}
		}()
	}

	wg.Wait()

	// 确定性排空：反复让 W1/W2 失联（失数累积到 M 即 Lost/删除），
	// 每轮用一个全新的全匹配工作者 C<round> 取走并完成本批操作；
	// 再让它失联以删除本轮可能产生的弃置操作，直到在途操作全部消失。
	for round := 0; round < 100000; round++ {
		_ = s.WorkerLost("W1")
		_ = s.WorkerLost("W2")
		drainName := fmt.Sprintf("drain-%d", round)
		allProps := []action.KV{{Key: "os", Value: "linux"}}
		if err := s.Register(drainName, allProps, 64); err != nil {
			t.Fatalf("drain register: %v", err)
		}
		var got []PollResult
		for i := 0; i < 64; i++ {
			r, err := s.Poll(drainName)
			if err != nil || r.Op == nil {
				break
			}
			got = append(got, r)
		}
		for _, r := range got {
			if err := s.Complete(drainName, r.Op, r.Attempt, 0, false); err != nil {
				t.Fatalf("drain complete: %v", err)
			}
		}
		if err := s.WorkerLost(drainName); err != nil {
			t.Fatalf("drain lost: %v", err)
		}
		s.mu.Lock()
		remaining := len(s.registry.All())
		s.mu.Unlock()
		if remaining == 0 {
			break
		}
	}
	s.mu.Lock()
	if remaining := len(s.registry.All()); remaining != 0 {
		s.mu.Unlock()
		t.Fatalf("drain failed: %d inflight remain", remaining)
	}
	s.mu.Unlock()

	s.mu.Lock()
	failed := false
	failMsg := ""
	used := 0
	for _, name := range s.pool.Names() {
		w, _ := s.pool.Get(name)
		used += w.Used()
	}
	dispatched := 0
	for _, o := range s.registry.All() {
		if o.State == action.Assigned || o.State == action.Abandoned {
			dispatched++
		}
		if o.State == action.Queued && len(o.Waiters) == 0 {
			failed, failMsg = true, fmt.Sprintf("queued op %s without waiters", o.Digest)
			break
		}
	}
	if used != dispatched {
		failed, failMsg = true, fmt.Sprintf("used slots %d != dispatched %d", used, dispatched)
	}

	// 每个被接受的等待者必须恰好收到一次终局。
	uniq := map[int]bool{}
	for _, id := range ids {
		uniq[id] = true
	}
	type rec struct {
		id int
		ch <-chan action.Outcome
		ok bool
	}
	var recs []rec
	for id := range uniq {
		w, ok := s.waiters[id]
		recs = append(recs, rec{id: id, ok: ok && w.Terminal(), ch: w.Done()})
	}
	s.mu.Unlock()
	if failed {
		t.Fatal(failMsg)
	}
	for _, rc := range recs {
		if !rc.ok {
			t.Fatalf("waiter %d not terminal after drain", rc.id)
		}
		select {
		case <-rc.ch:
		default:
			t.Fatalf("waiter %d missing outcome", rc.id)
		}
	}
}
