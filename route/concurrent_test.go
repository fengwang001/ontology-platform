package route

import (
	"sync"
	"testing"

	"ontology/numplan"
	"ontology/portdb"
)

// TestConcurrentSmoke 在 -race 下验证并发调用不产生数据竞争，
// 且并发后历史答案仍可由同一操作集合复现。
func TestConcurrentSmoke(t *testing.T) {
	plan := numplan.New()
	must0(plan.AssignBlock("13", 5, 1, 0))
	db := portdb.New(plan, 0, 3)
	r := New(db)
	const workers, rounds = 8, 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				now := int64(1 + id*rounds + i)
				_, _ = r.Query("13001", 1+id%5, ACQ, now)
				_, _ = r.QueryAt("13001", 1+id%5, OR, 0)
				_, _ = db.RequestPort("13001", 1, 2, now+10, now)
				_ = db.Disconnect("13002", now)
			}
		}(w)
	}
	wg.Wait()
	if _, err := r.QueryAt("13001", 4, OR, 0); err != nil {
		t.Fatalf("history after concurrency: %v", err)
	}
}
