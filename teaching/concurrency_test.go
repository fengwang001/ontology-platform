package teaching

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentEquivalence 并发调用必须等价于某个串行顺序：
// 多协程同时对守恒任务做指派，成功/拒绝的总量必须一致且守恒不被破坏。
func TestConcurrentEquivalence(t *testing.T) {
	s := newSvc(t)
	const nTeachers, nTasks = 8, 4
	for i := 0; i < nTeachers; i++ {
		must(t, s.AddTeacher(fmt.Sprintf("h%d", i), "wide", 1))
	}
	for p := 1; p <= nTasks; p++ {
		must(t, s.AddTask(TaskSpec{
			ID: fmt.Sprintf("task%d", p), Semester: "2024-1",
			Hours: 10, ClassSize: 30,
			WeekStart: 1, WeekEnd: 10, Periods: []int{p},
		}, 2))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount, failCount := 0, 0
	// 每个教师都尝试抢全部四个任务；同一任务只有恰好一个人能守恒成功。
	for i := 0; i < nTeachers; i++ {
		for p := 1; p <= nTasks; p++ {
			wg.Add(1)
			go func(i, p int) {
				defer wg.Done()
				err := s.Assign([]AssignmentReq{{
					TeacherID: fmt.Sprintf("h%d", i),
					TaskID:    fmt.Sprintf("task%d", p),
					Hours:     10,
				}}, 10)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					okCount++
					return
				}
				switch codeOf(err) {
				case ErrHoursNotConserved, ErrIllegalState:
					failCount++
				default:
					t.Errorf("unexpected concurrent error: %v", err)
				}
			}(i, p)
		}
	}
	wg.Wait()

	if okCount != nTasks {
		t.Fatalf("exactly one winner per task expected: ok=%d", okCount)
	}
	if okCount+failCount != nTeachers*nTasks {
		t.Fatalf("lost operations: %d+%d != %d", okCount, failCount, nTeachers*nTasks)
	}
	for p := 1; p <= nTasks; p++ {
		n, err := s.TaskAllocated(fmt.Sprintf("task%d", p), 11)
		must(t, err)
		if n != 10 {
			t.Fatalf("task%d allocated %d, want 10 (conservation)", p, n)
		}
	}

	// 待确认指派并发确认：恰好成功的 nTasks 条可确认，其余不存在/状态错误。
	var wg2 sync.WaitGroup
	confirmed := 0
	for i := 0; i < nTeachers; i++ {
		for p := 1; p <= nTasks; p++ {
			wg2.Add(1)
			go func(i, p int) {
				defer wg2.Done()
				if err := s.Respond(fmt.Sprintf("h%d", i), fmt.Sprintf("task%d", p), true, 12); err == nil {
					mu.Lock()
					confirmed++
					mu.Unlock()
				}
			}(i, p)
		}
	}
	wg2.Wait()
	if confirmed != nTasks {
		t.Fatalf("confirmed=%d, want %d", confirmed, nTasks)
	}
}

// TestDeterministicReplay 相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	run := func() []string {
		s := newSvc(t)
		var out []string
		step := func(name string, err error) {
			out = append(out, name+":"+errCodeIndex(err))
		}
		must(t, s.AddTeacher("a", "wide", 1))
		must(t, s.AddTeacher("b", "prof", 2))
		step("addt1", s.AddTask(TaskSpec{
			ID: "c1", Semester: "2024-1", Hours: 20, ClassSize: 60,
			NewCourse: true, IsLab: true,
			WeekStart: 1, WeekEnd: 5, Periods: []int{1, 2},
		}, 3))
		step("assign", s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 20}}, 4))
		step("assign-dup", s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 20}}, 5))
		step("respond", s.Respond("a", "c1", true, 14))
		step("replace", s.Replace("a", "c1", "b", 3, 15))
		r, err := s.Settle("a", "2024-1", 16)
		must(t, err)
		out = append(out, fmt.Sprintf("settle-a=%+v", r))
		r, err = s.Settle("b", "2024-1", 17)
		must(t, err)
		out = append(out, fmt.Sprintf("settle-b=%+v", r))
		step("settle-again", func() error { _, e := s.Settle("a", "2024-1", 999); return e }())
		step("frozen-replace", s.Replace("a", "c1", "b", 2, 1000))
		return out
	}
	first := run()
	second := run()
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("replay differs:\n%v\nvs\n%v", first, second)
	}
}

// BenchmarkConflictLookup 证明冲突判定开销不随历史任务总数增长：
// 预载 N 个历史任务后，一次冲突指派的耗时应与 N 近似无关（仅 O(新任务格数)）。
func BenchmarkConflictLookup(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			probe := func(history int) func(b *testing.B) {
				return func(b *testing.B) {
					s, _ := NewService(testConfig(), 0)
					var clock int64 = 1
					if err := s.AddTeacher("a", "giant", clock); err != nil {
						b.Fatal(err)
					}
					// 历史任务：每周 1 节次，节次编号互不相同 => 互不冲突。
					for i := 0; i < history; i++ {
						clock++
						tid := fmt.Sprintf("old%d", i)
						spec := TaskSpec{
							ID: tid, Semester: "2024-1",
							Hours: 2, ClassSize: 30,
							WeekStart: 1, WeekEnd: 2, Periods: []int{100 + i},
						}
						if err := s.AddTask(spec, clock); err != nil {
							b.Fatal(err)
						}
						clock++
						if err := s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: tid, Hours: 2}}, clock); err != nil {
							b.Fatal(err)
						}
						clock++
						if err := s.Respond("a", tid, true, clock); err != nil {
							b.Fatal(err)
						}
					}
					// 探测任务每次用新 ID（避免状态错误短路），与历史任务同周；
					// 判定走到占用表查询，冲突路径成本即本基准度量对象。
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						clock++
						pid := fmt.Sprintf("probe%d", i)
						spec := TaskSpec{
							ID: pid, Semester: "2024-1",
							Hours: 2, ClassSize: 30,
							WeekStart: 1, WeekEnd: 2, Periods: []int{100},
						}
						if err := s.AddTask(spec, clock); err != nil {
							b.Fatal(err)
						}
						clock++
						err := s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: pid, Hours: 2}}, clock)
						if codeOf(err) != ErrSlotConflict {
							b.Fatalf("unexpected: %v", err)
						}
					}
				}
			}(n)
			probe(b)
		})
	}
}
