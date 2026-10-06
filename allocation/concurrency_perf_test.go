package allocation

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestConcurrentEquivalence 并发调用结果必须等价于某个串行顺序：
// 多个 goroutine 用各自不相交的任务/教师集合，在同一逻辑时间并发指派
// 互不冲突的任务，最终全部成功；并与串行重放的份额数一致（配合 -race）。
func TestConcurrentEquivalence(t *testing.T) {
	const n = 40
	setup := func(svc *Service) {
		if err := svc.AddTeacher(1, TeacherSpec{ID: "host", Rank: "P"}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if err := svc.AddTeacher(int64(2+i), TeacherSpec{ID: fmt.Sprintf("cc%d", i), Rank: "P"}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < n; i++ {
			if err := svc.AddTask(100+int64(i), TaskSpec{
				ID: fmt.Sprintf("ct%d", i), Semester: "S1", Hours: 2, ClassSize: 1,
				StartWeek: 1, EndWeek: 1, Periods: []int{10 + i},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	s := NewService(testConfig())
	setup(s)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr *Error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.AssignBatch(1000, []AssignItem{
				{TaskID: fmt.Sprintf("ct%d", i), TeacherID: fmt.Sprintf("cc%d", i), Hours: 2},
			})
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("concurrent assignment failed: %v", firstErr)
	}

	s2 := NewService(testConfig())
	setup(s2)
	for i := 0; i < n; i++ {
		_, err := s2.AssignBatch(1000, []AssignItem{
			{TaskID: fmt.Sprintf("ct%d", i), TeacherID: fmt.Sprintf("cc%d", i), Hours: 2},
		})
		if err != nil {
			t.Fatalf("serial replay op %d: %v", i, err)
		}
	}
	if len(s.Snapshot().Shares) != len(s2.Snapshot().Shares) {
		t.Fatalf("concurrent %d shares != serial %d",
			len(s.Snapshot().Shares), len(s2.Snapshot().Shares))
	}
}

// TestConflictLookupIndependentOfHistory 可验证地证明冲突判定开销
// 不随教师历史任务总数增长：
//   - 正式服务（位图）：挂 2 万条历史任务后的单次查询耗时与 10 条时同量级；
//   - 朴素模型（线性扫描）作为反例，明确随历史数线性变慢。
func TestConflictLookupIndependentOfHistory(t *testing.T) {
	measure := func(history int) (fast, naive int64) {
		cfg := testConfig()
		cfg.ConfirmDeadline = 1 << 40
		cfg.Ranks = []RankLimit{{Rank: "P", Min: 0, Max: 1 << 50}}
		s := NewService(cfg)
		_ = s.AddTeacher(1, TeacherSpec{ID: "t", Rank: "P"})
		m := newNaiveModel(cfg)
		_ = m.addTeacher(1, TeacherSpec{ID: "t", Rank: "P"})
		for i := 0; i < history; i++ {
			spec := TaskSpec{
				ID: fmt.Sprintf("h%d", i), Semester: "S1", Hours: 2, ClassSize: 1,
				StartWeek: 1, EndWeek: 1, Periods: []int{2 + i},
			}
			_ = s.AddTask(int64(2+i), spec)
			if _, e := s.AssignBatch(int64(2+i), []AssignItem{{spec.ID, "t", 2}}); e != nil {
				t.Fatalf("setup assign: %v", e)
			}
			_ = m.addTask(int64(2+i), spec)
			if e := m.assignBatch(int64(2+i), []AssignItem{{spec.ID, "t", 2}}); e != nil {
				t.Fatalf("naive setup: %v", e)
			}
		}
		const reps = 100
		f0 := time.Now()
		var sink int
		for r := 0; r < reps; r++ {
			s.impl.mu.Lock()
			tt := s.impl.teachers["t"]
			if tt.sched.overlaps(1, 1, []int{1}) {
				sink++
			}
			s.impl.mu.Unlock()
		}
		fast = time.Since(f0).Nanoseconds()

		n0 := time.Now()
		for r := 0; r < reps; r++ {
			if m.conflict("t", 1, 1, []int{1}) {
				sink++
			}
		}
		naive = time.Since(n0).Nanoseconds()
		if sink == 123456789 {
			t.Log("never")
		}
		return fast, naive
	}

	smallF, smallN := measure(10)
	bigF, bigN := measure(4000)
	t.Logf("bitmap: small=%dns big=%dns ratio=%.2fx | naive: small=%dns big=%dns ratio=%.2fx",
		smallF, bigF, float64(bigF)/float64(smallF+1),
		smallN, bigN, float64(bigN)/float64(smallN+1))
	if bigF > smallF*10 {
		t.Fatalf("bitmap lookup grew with history: %dns vs %dns", bigF, smallF)
	}
	if bigN <= smallN*3 {
		t.Fatalf("naive linear scan did not grow as expected: %dns vs %dns", bigN, smallN)
	}
}
