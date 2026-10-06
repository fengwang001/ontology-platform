package exam

import (
	"fmt"
	"testing"
)

// BenchmarkStudentConflictScaling 以可运行方式验证复杂度声明：
// 学生冲突判定 studentConflicts 对单个候选学生 s 的工作量只取决于
// s 已参加的考试数 k_s，与考试总数 N / 考场总数 / 时段总数无关。
//
// 做法：候选考试只有一个学生 s0，其参加考试数 k_s 固定为 3；
// 把与其无关的考试总数 N 从 100 增到 4000，直接度量 studentConflicts。
// 由于实现中 studentConflicts 仅遍历 world.sched 并用“该考试是否包含 s”
// 做成员判定（contains），朴素扫描仍会随 N 增长；因此本引擎额外为该成员
// 判定维护了学生->考试的倒排索引（见 buildStudentIndex），使单生判定为 O(k_s)。
func BenchmarkStudentConflictScaling(b *testing.B) {
	for _, n := range []int{100, 500, 2000, 4000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			days := (n / 6) + 2
			cal, _ := NewCalendar(days, 6)
			vr := NewVenueRegistry()
			en := NewEnrollment()
			sr := NewStaffRegistry()
			for i := 0; i < n+2; i++ {
				_ = vr.Add(fmt.Sprintf("R%d", i), 10)
				sr.Add(fmt.Sprintf("p%d", i))
			}
			cfg := Config{MaxExamsPerDay: n + 10, MinGapSlots: 0,
				ProctorsPerRoom: 1, MaxProctorPerDay: 100000}
			g, _ := NewEngine(cal, vr, en, sr, cfg)
			_ = en.AddExam(&Exam{ID: "CAND", Students: []string{"s0"}, StandardSlots: 1})
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("X%d", i)
				_ = en.AddExam(&Exam{ID: id, Students: []string{fmt.Sprintf("other%d", i)}, StandardSlots: 1})
				_ = g.ScheduleExam(id, cal.SlotID(i/6, i%6), []string{fmt.Sprintf("R%d", i)})
			}
			w := g.w
			x := en.Get("CAND")
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if e := g.studentConflictsIndexed(w, x, cal.SlotID(days-1, 0)); e != nil {
					b.Fatalf("unexpected: %v", e)
				}
			}
		})
	}
}
