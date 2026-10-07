package sheet_test

import (
	"fmt"
	"testing"
	"time"

	"ontology/sheet"
)

// 两档表格规模, 相差两个数量级。
const (
	scaleSmall = 10_000
	scaleLarge = 1_000_000
)

// buildScaledService 预填 n 个单元格, 返回服务与递增的 now 计数器。
func buildScaledService(b testing.TB, n int) (*sheet.Service, *int64) {
	b.Helper()
	s, err := sheet.New(map[string]int{"worker": 10})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	now := int64(0)
	for base := 0; base < n; base += sheet.MaxEdits {
		end := base + sheet.MaxEdits
		if end > n {
			end = n
		}
		edits := make([]sheet.Edit, 0, end-base)
		for i := base; i < end; i++ {
			edits = append(edits, sheet.Edit{Key: fmt.Sprintf("cell%08d", i), Value: int64(i)})
		}
		now++
		if r := s.Apply("worker", edits, now); !r.OK {
			b.Fatalf("预填失败: %v", r.Code)
		}
	}
	return s, &now
}

// workEdits 生成作用于固定工作集（表格尾部 10 个单元格）的编辑,
// value 随序号变化以保证编辑始终有效。
func workEdits(n int, seq int64) []sheet.Edit {
	edits := make([]sheet.Edit, 10)
	for j := range edits {
		edits[j] = sheet.Edit{Key: fmt.Sprintf("cell%08d", n-1-j), Value: seq}
	}
	return edits
}

// measureOps 在固定工作集上循环执行 Apply/Undo/Redo,
// 分别返回三类操作的平均耗时（纳秒）。
func measureOps(s *sheet.Service, now *int64, nCells, iters int) (applyNs, undoNs, redoNs float64) {
	var applyDur, undoDur, redoDur time.Duration
	for i := 0; i < iters; i++ {
		*now++
		edits := workEdits(nCells, int64(i)+1)
		start := time.Now()
		r := s.Apply("worker", edits, *now)
		applyDur += time.Since(start)
		if !r.OK {
			panic(fmt.Sprintf("Apply 被拒: %v", r.Code))
		}
		*now++
		start = time.Now()
		r = s.Undo("worker", *now)
		undoDur += time.Since(start)
		if !r.OK {
			panic(fmt.Sprintf("Undo 被拒: %v", r.Code))
		}
		*now++
		start = time.Now()
		r = s.Redo("worker", *now)
		redoDur += time.Since(start)
		if !r.OK {
			panic(fmt.Sprintf("Redo 被拒: %v", r.Code))
		}
	}
	return float64(applyDur.Nanoseconds()) / float64(iters),
		float64(undoDur.Nanoseconds()) / float64(iters),
		float64(redoDur.Nanoseconds()) / float64(iters)
}

// 复杂度证明：Apply/Undo/Redo 的开销只与事务内单元格数有关,
// 不随表格总规模增长。两档规模相差 100 倍, 若复杂度与总规模相关,
// 耗时应相差约 100 倍; 实测比值应接近 1。取 5 次测量的最小值以降噪。
func TestPerformanceScaleIndependence(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过性能对照")
	}
	const iters = 2000
	const repeats = 5
	// 宽容的上界：远低于规模比 100, 足以证伪“随总规模增长”。
	const maxRatio = 4.0

	type sample struct{ apply, undo, redo float64 }
	measure := func(nCells int) sample {
		s, now := buildScaledService(t, nCells)
		var best sample
		for r := 0; r < repeats; r++ {
			a, u, re := measureOps(s, now, nCells, iters)
			if r == 0 || a < best.apply {
				best.apply = a
			}
			if r == 0 || u < best.undo {
				best.undo = u
			}
			if r == 0 || re < best.redo {
				best.redo = re
			}
		}
		return best
	}

	small := measure(scaleSmall)
	large := measure(scaleLarge)
	t.Logf("性能对照（事务=10 单元格, %d 次迭代取最小均值）:", iters)
	t.Logf("  规模 %8d: Apply=%.0fns Undo=%.0fns Redo=%.0fns", scaleSmall, small.apply, small.undo, small.redo)
	t.Logf("  规模 %8d: Apply=%.0fns Undo=%.0fns Redo=%.0fns", scaleLarge, large.apply, large.undo, large.redo)

	ratios := []struct {
		name string
		lo   float64
		hi   float64
	}{
		{"Apply", small.apply, large.apply},
		{"Undo", small.undo, large.undo},
		{"Redo", small.redo, large.redo},
	}
	for _, rc := range ratios {
		ratio := rc.hi / rc.lo
		t.Logf("  %s 大/小规模耗时比 = %.2f (上界 %.0f)", rc.name, ratio, maxRatio)
		if ratio > maxRatio {
			t.Fatalf("%s 耗时随表格规模增长: 比值 %.2f 超过上界 %.0f", rc.name, ratio, maxRatio)
		}
	}
}

// BenchmarkApplyUndoRedoAtScale 供 go test -bench 精确对照两档规模。
func BenchmarkApplyUndoRedoAtScale(b *testing.B) {
	for _, n := range []int{scaleSmall, scaleLarge} {
		b.Run(fmt.Sprintf("cells=%d", n), func(b *testing.B) {
			s, now := buildScaledService(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				*now++
				if r := s.Apply("worker", workEdits(n, int64(i)+1), *now); !r.OK {
					b.Fatalf("Apply 被拒: %v", r.Code)
				}
				*now++
				if r := s.Undo("worker", *now); !r.OK {
					b.Fatalf("Undo 被拒: %v", r.Code)
				}
				*now++
				if r := s.Redo("worker", *now); !r.OK {
					b.Fatalf("Redo 被拒: %v", r.Code)
				}
			}
		})
	}
}
