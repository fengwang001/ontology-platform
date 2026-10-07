package qc

import (
	"testing"
	"time"
)

// 本文件以两档规模对照验证两条复杂度承诺：
//  1. 运行判定开销不随项目历史运行总数增长（O(1)）；
//  2. 失控追溯标记开销只与待标记报告数相关，不随历史报告总数增长。
//
// 运行方式：
//   go test ./qc -run TestScalingCheck -v      # 打印两档规模对照表并断言
//   go test ./qc -bench . -benchtime 1s        # 标准基准输出

const (
	scaleSmall = 10_000
	scaleLarge = 200_000
	segmentLen = 100 // 每次失控追溯待标记的报告数（两档相同）
)

// fillRuns 以正常运行（偏离量为零）填充项目历史。
func fillRuns(b testing.TB, s *System, n int) {
	b.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.Run("I1", "A1", 100, 200, int64(i)); err != nil {
			b.Fatalf("fill run %d: %v", i, err)
		}
	}
}

func benchRunJudgment(b *testing.B, history int) {
	s := New()
	if err := s.RegisterAnalyte("I1", "A1", 100, 10, 200, 20, 1_000_000_000); err != nil {
		b.Fatal(err)
	}
	fillRuns(b, s, history)
	now := int64(history)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		if _, err := s.Run("I1", "A1", 100, 200, now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRunJudgmentHistory10K(b *testing.B)  { benchRunJudgment(b, scaleSmall) }
func BenchmarkRunJudgmentHistory200K(b *testing.B) { benchRunJudgment(b, scaleLarge) }

// setupMarking 构造一个项目：history 份历史报告 + segmentLen 份待标记报告，
// 返回系统与待标记段报告单号。项目处于在控，下一次失控运行将触发追溯。
func setupMarking(b testing.TB, history, segment int) (*System, []int64) {
	b.Helper()
	s := New()
	if err := s.RegisterAnalyte("I1", "A1", 100, 10, 200, 20, 1_000_000_000); err != nil {
		b.Fatal(err)
	}
	now := int64(0)
	run := func(low int64) {
		now++
		if _, err := s.Run("I1", "A1", low, 200, now); err != nil {
			b.Fatal(err)
		}
	}
	issue := func() int64 {
		now++
		id, err := s.IssueReport("I1", "A1", now)
		if err != nil {
			b.Fatal(err)
		}
		return id
	}
	run(100) // 首次正常运行，允许出具
	for i := 0; i < history; i++ {
		issue()
	}
	run(100) // 最近一次非失控运行：之前的报告不在追溯区间
	seg := make([]int64, 0, segment)
	for i := 0; i < segment; i++ {
		seg = append(seg, issue())
	}
	return s, seg
}

// measureMarking 反复执行“校准恢复 -> 复位报告状态 -> 失控运行追溯”，
// 返回单次追溯标记的平均耗时。
func measureMarking(b testing.TB, history, segment, reps int) time.Duration {
	b.Helper()
	var total time.Duration
	var now int64 = 1_000_000
	for r := 0; r < reps; r++ {
		s, seg := setupMarking(b, history, segment)
		now += int64(history + segment + 10)
		start := time.Now()
		if _, err := s.Run("I1", "A1", 131, 200, now); err != nil { // 失控，触发追溯
			b.Fatal(err)
		}
		total += time.Since(start)
		for _, id := range seg { // 校验标记确实发生
			st, err := s.ReportStatus(id)
			if err != nil || st != ReportPendingReview {
				b.Fatalf("report %d not marked: %v %v", id, st, err)
			}
		}
	}
	return total / time.Duration(reps)
}

// measureRunJudgment 在已有 history 次运行的项目上再执行 reps 次运行，返回单次平均耗时。
func measureRunJudgment(b testing.TB, history, reps int) time.Duration {
	b.Helper()
	s := New()
	if err := s.RegisterAnalyte("I1", "A1", 100, 10, 200, 20, 1_000_000_000); err != nil {
		b.Fatal(err)
	}
	fillRuns(b, s, history)
	now := int64(history)
	start := time.Now()
	for i := 0; i < reps; i++ {
		now++
		if _, err := s.Run("I1", "A1", 100, 200, now); err != nil {
			b.Fatal(err)
		}
	}
	return time.Since(start) / time.Duration(reps)
}

// TestScalingCheck 两档规模对照：判定与追溯的单次耗时不得随历史总量显著增长。
func TestScalingCheck(t *testing.T) {
	jSmall := measureRunJudgment(t, scaleSmall, 50_000)
	jLarge := measureRunJudgment(t, scaleLarge, 50_000)
	mSmall := measureMarking(t, scaleSmall, segmentLen, 30)
	mLarge := measureMarking(t, scaleLarge, segmentLen, 30)
	t.Logf("运行判定单次耗时：历史 %d 次 -> %v；历史 %d 次 -> %v（比值 %.2f）",
		scaleSmall, jSmall, scaleLarge, jLarge, float64(jLarge)/float64(jSmall))
	t.Logf("追溯标记单次耗时（待标记 %d 份）：历史报告 %d 份 -> %v；历史报告 %d 份 -> %v（比值 %.2f）",
		segmentLen, scaleSmall, mSmall, scaleLarge, mLarge, float64(mLarge)/float64(mSmall))
	// 规模放大 20 倍，若实现为 O(历史总量) 则比值应接近 20；
	// O(1) 实现下比值应接近 1，留出充足噪声余量。
	if jLarge > jSmall*3 {
		t.Fatalf("run judgment cost grows with history: %v -> %v", jSmall, jLarge)
	}
	if mLarge > mSmall*5 {
		t.Fatalf("marking cost grows with total reports: %v -> %v", mSmall, mLarge)
	}
}

func BenchmarkRetroactiveMarking(b *testing.B) {
	for _, history := range []int{scaleSmall, scaleLarge} {
		b.Run(map[int]string{scaleSmall: "Reports10K", scaleLarge: "Reports200K"}[history], func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				s, _ := setupMarking(b, history, segmentLen)
				b.StartTimer()
				if _, err := s.Run("I1", "A1", 131, 200, 1_000_000_000); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
