package ontology

import (
	"errors"
	"sync"
	"testing"
)

// N 个 goroutine 对同一主键并发「读-改-写」：成功次数必须恰好
// 等于最终版本号减一，失败全部是版本冲突，且版本号连续无跳号。
func TestConcurrentUpdatesInvariant(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "hot", map[string]any{"n": 0}); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				cur, err := s.Get("Robot", "hot")
				if err != nil {
					t.Errorf("get: %v", err)
					return
				}
				_, err = s.Update("Robot", "hot", cur.Version, map[string]any{"n": cur.Version})
				if err == nil {
					mu.Lock()
					successes++
					mu.Unlock()
					return
				}
				var vc *VersionConflictError
				if !errors.As(err, &vc) {
					t.Errorf("err = %v, want VersionConflictError", err)
					return
				}
				// 版本冲突：重试直到成功。
			}
		}()
	}
	wg.Wait()

	final, err := s.Get("Robot", "hot")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := successes, int(final.Version)-1; got != want {
		t.Fatalf("successes = %d, final version = %d, want successes == version-1 (%d)",
			got, final.Version, want)
	}
}

// 盲写（固定期望版本 1）下，恰好一个成功，其余全是版本冲突。
func TestConcurrentBlindUpdatesExactlyOneWins(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "hot", nil); err != nil {
		t.Fatal(err)
	}

	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes, conflicts := 0, 0

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update("Robot", "hot", 1, nil)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
				return
			}
			var vc *VersionConflictError
			if !errors.As(err, &vc) {
				t.Errorf("err = %v, want VersionConflictError", err)
				return
			}
			conflicts++
		}()
	}
	wg.Wait()

	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("successes = %d, conflicts = %d, want 1 and %d", successes, conflicts, workers-1)
	}
	if got, _ := s.Get("Robot", "hot"); got.Version != 2 {
		t.Fatalf("final version = %d, want 2", got.Version)
	}
}

// BatchWrite 与单条写并发：批次整体串行生效，任何读方都观察不到
// 批次的中间状态。p、q 只由「同时更新两者」的批次推进，因此
// 最终 p.Version == q.Version == 1 + 成功批次数；若有任何一个批次
// 被部分应用，该等式必然被破坏。x 由并发单条写推进，制造真实交错。
func TestBatchAtomicityUnderConcurrency(t *testing.T) {
	s := newTestStore()
	for _, pk := range []string{"p", "q", "x"} {
		if _, err := s.Create("Robot", pk, nil); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	batchSuccesses := 0

	// 并发单条写 x，与批次交错。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 400; i++ {
			cur, err := s.Get("Robot", "x")
			if err != nil {
				t.Errorf("get x: %v", err)
				return
			}
			if _, err := s.Update("Robot", "x", cur.Version, nil); err != nil {
				var vc *VersionConflictError
				if !errors.As(err, &vc) {
					t.Errorf("update x: %v", err)
					return
				}
			}
		}
	}()

	// 多个写者并发提交「同时推 p、q」的批次，冲突时重试。
	const batchWriters = 8
	const batchesPerWriter = 25
	for w := 0; w < batchWriters; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < batchesPerWriter; i++ {
				for {
					p, err1 := s.Get("Robot", "p")
					q, err2 := s.Get("Robot", "q")
					if err1 != nil || err2 != nil {
						t.Errorf("get: %v %v", err1, err2)
						return
					}
					err := s.BatchWrite([]WriteOp{
						{Kind: OpUpdate, ObjectType: "Robot", PrimaryKey: "p", ExpectedVersion: p.Version},
						{Kind: OpUpdate, ObjectType: "Robot", PrimaryKey: "q", ExpectedVersion: q.Version},
					})
					if err == nil {
						mu.Lock()
						batchSuccesses++
						mu.Unlock()
						break
					}
					var vc *VersionConflictError
					if !errors.As(err, &vc) {
						t.Errorf("batch err = %v, want VersionConflictError", err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	p, _ := s.Get("Robot", "p")
	q, _ := s.Get("Robot", "q")
	want := int64(1 + batchSuccesses)
	if p.Version != want || q.Version != want {
		t.Fatalf("p=%d q=%d, want both %d (1 + %d successful batches)",
			p.Version, q.Version, want, batchSuccesses)
	}
}
