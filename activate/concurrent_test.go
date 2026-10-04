package activate_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/activate"
)

// 同一 sn 并发首次激活：恰有一个指纹绑定成功；同指纹得到幂等结果，异指纹 ErrConflict。
func TestConcurrentFirstActivateSingleSn(t *testing.T) {
	s := newSvc(t, 10, 1000)
	_ = s.Roster().AddTenant("t", 1)
	_ = s.Roster().RegisterBatch("b", "t", 1_000_000_000_000,
		[][]byte{bs("D")}, 0)

	const n = 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	boundFPS := map[string]int{}
	successIDs := map[int64]int{}
	conflicts, replays := 0, 0
	firstBinds := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 全部同指纹：恰有一个首次绑定，其余均为幂等重放。
			fp := "f0"
			// 并发请求使用同一时刻：实现须等价于某串行顺序（见规格）。
			r, err := s.Activate(bs("D"), bs(fp), 10)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				boundFPS[fp]++
				successIDs[r.ID]++
				if r.Replayed {
					replays++
				} else {
					firstBinds++
				}
				if r.ID != 1 || r.Gen != 1 {
					t.Errorf("unexpected id/gen %d/%d", r.ID, r.Gen)
				}
			case errors.Is(err, activate.ErrConflict):
				conflicts++
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}(i)
	}
	wg.Wait()

	rec := s.Roster().Get(bs("D"))
	if rec == nil || rec.State != 1 || rec.ID != 1 || rec.Gen != 1 {
		t.Fatalf("final rec=%+v", rec)
	}
	if len(boundFPS) != 1 {
		t.Fatalf("bound fps=%v want exactly 1", boundFPS)
	}
	if len(successIDs) != 1 || successIDs[1] == 0 {
		t.Fatalf("success ids=%v", successIDs)
	}
	if firstBinds != 1 {
		t.Fatalf("first binds=%d want 1", firstBinds)
	}
	if replays != n-1 {
		t.Fatalf("replays=%d want %d", replays, n-1)
	}
	if conflicts != 0 {
		t.Fatalf("conflicts=%d want 0 (same fp)", conflicts)
	}
}

// 已绑定后并发：同指纹得幂等重放，异指纹（数量<M）得 ErrConflict，互不串扰。
func TestConcurrentMixedFingerprints(t *testing.T) {
	s := newSvc(t, 10, 1000)
	_ = s.Roster().AddTenant("t", 1)
	_ = s.Roster().RegisterBatch("b", "t", 1_000_000_000_000,
		[][]byte{bs("D")}, 0)
	if r, err := s.Activate(bs("D"), bs("f0"), 10); err != nil || r.ID != 1 {
		t.Fatal(err)
	}

	const same, diff = 100, 4
	var wg sync.WaitGroup
	replays, conflicts, others := 0, 0, 0
	var mu sync.Mutex
	run := func(fp string) {
		defer wg.Done()
		_, err := s.Activate(bs("D"), bs(fp), 20)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			replays++
		case errors.Is(err, activate.ErrConflict):
			conflicts++
		default:
			others++
			t.Errorf("unexpected err %v", err)
		}
	}
	for i := 0; i < same; i++ {
		wg.Add(1)
		go run("f0")
	}
	for i := 0; i < diff; i++ {
		wg.Add(1)
		go run(fmt.Sprintf("fx%d", i))
	}
	wg.Wait()
	if replays != same || conflicts != diff || others != 0 {
		t.Fatalf("replays=%d conflicts=%d others=%d", replays, conflicts, others)
	}
	// 异指纹累计 4 < M=10，未锁定；gen 仍为 1。
	rec := s.Roster().Get(bs("D"))
	if rec.Gen != 1 {
		t.Fatalf("gen=%d want 1", rec.Gen)
	}
}

// 并发激活多个 sn、租户名额有限：占用绝不超 N，id 连续无洞。
func TestConcurrentQuotaAndIDs(t *testing.T) {
	s := newSvc(t, 10, 1000)
	const quota = 13
	_ = s.Roster().AddTenant("t", quota)
	const total = 120
	sns := make([][]byte, total)
	for i := range sns {
		sns[i] = bs(fmt.Sprintf("dev%03d", i))
	}
	if err := s.Roster().RegisterBatch("b", "t", 1_000_000_000_000, sns, 0); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	idSeen := map[int64]bool{}
	var idMu sync.Mutex
	successes := make(chan int64, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.Activate(sns[i], bs("hw"), 100)
			if err == nil {
				idMu.Lock()
				if idSeen[r.ID] {
					t.Errorf("dup id %d", r.ID)
				}
				idSeen[r.ID] = true
				idMu.Unlock()
				successes <- r.ID
			} else if !errors.Is(err, activate.ErrQuota) {
				t.Errorf("unexpected err: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(successes)

	if len(successes) != quota {
		t.Fatalf("activated=%d want quota=%d", len(successes), quota)
	}
	minID, maxID := int64(1<<62), int64(0)
	for id := range successes {
		if id < minID {
			minID = id
		}
		if id > maxID {
			maxID = id
		}
	}
	if minID != 1 || maxID != quota {
		t.Fatalf("id range %d..%d want 1..%d", minID, maxID, quota)
	}
}
