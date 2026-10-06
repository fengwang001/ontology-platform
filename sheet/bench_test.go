package sheet

// 性能对照：证明 Apply/Undo/Redo 的开销只与事务内单元格数有关，
// 与表格总规模无关。方法：在相差两个数量级的两档表格规模
// （10^4 与 10^6 个单元格）上执行相同的事务负载，比较单操作耗时。

import (
	"fmt"
	"testing"
	"time"
)

// fillCells 以 50 条编辑一批的事务填充 cells 个单元格。
func fillCells(s *Service, user string, cells int, now *int64) {
	for base := 0; base < cells; base += MaxEdits {
		edits := make([]Edit, 0, MaxEdits)
		for j := 0; j < MaxEdits && base+j < cells; j++ {
			edits = append(edits, Edit{Key: fmt.Sprintf("k%d", base+j), Value: 1})
		}
		*now++
		if r := s.Apply(user, edits, *now); r.Code != OK {
			panic(fmt.Sprintf("fill Apply: %+v", r))
		}
	}
}

// makeEdits 生成一批落在 [0, cells) 键空间内、按起始点旋转的编辑。
func makeEdits(cells, start, n int) []Edit {
	edits := make([]Edit, 0, n)
	for j := 0; j < n; j++ {
		edits = append(edits, Edit{Key: fmt.Sprintf("k%d", (start+j)%cells), Value: int64(start + j)})
	}
	return edits
}

func benchmarkApply(b *testing.B, cells int) {
	s, err := NewService(map[string]int{"u": MaxDepth})
	if err != nil {
		b.Fatal(err)
	}
	var now int64
	fillCells(s, "u", cells, &now)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		s.Apply("u", makeEdits(cells, i*MaxEdits, MaxEdits), now)
	}
}

func BenchmarkApply_1e4Cells(b *testing.B) { benchmarkApply(b, 10_000) }
func BenchmarkApply_1e6Cells(b *testing.B) { benchmarkApply(b, 1_000_000) }

func benchmarkUndoRedo(b *testing.B, cells int) {
	s, err := NewService(map[string]int{"u": MaxDepth})
	if err != nil {
		b.Fatal(err)
	}
	var now int64
	fillCells(s, "u", cells, &now)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		s.Apply("u", makeEdits(cells, i*MaxEdits, MaxEdits), now)
		now++
		s.Undo("u", now)
		now++
		s.Redo("u", now)
	}
}

func BenchmarkUndoRedo_1e4Cells(b *testing.B) { benchmarkUndoRedo(b, 10_000) }
func BenchmarkUndoRedo_1e6Cells(b *testing.B) { benchmarkUndoRedo(b, 1_000_000) }

// TestPerfScaleInvariance 以可执行断言对照两档规模：
// 相同事务负载下，10^6 规模表格的单操作耗时不得显著高于 10^4 规模
// （宽松上限 10 倍，用于容忍缓存与调度噪声；理论上应接近 1）。
func TestPerfScaleInvariance(t *testing.T) {
	const ops = 2000
	measure := func(cells int) time.Duration {
		s, err := NewService(map[string]int{"u": MaxDepth})
		if err != nil {
			t.Fatal(err)
		}
		var now int64
		fillCells(s, "u", cells, &now)
		start := time.Now()
		for i := 0; i < ops; i++ {
			now++
			if r := s.Apply("u", makeEdits(cells, i*MaxEdits, MaxEdits), now); r.Code != OK {
				t.Fatalf("Apply: %+v", r)
			}
			now++
			if r := s.Undo("u", now); r.Code != OK {
				t.Fatalf("Undo: %+v", r)
			}
			now++
			if r := s.Redo("u", now); r.Code != OK {
				t.Fatalf("Redo: %+v", r)
			}
		}
		return time.Since(start) / (3 * ops)
	}
	small := measure(10_000)
	large := measure(1_000_000)
	ratio := float64(large) / float64(small)
	t.Logf("单操作均耗：10^4 单元格表格 = %v，10^6 单元格表格 = %v，比值 = %.2f（上限 10）",
		small, large, ratio)
	if ratio > 10 {
		t.Fatalf("开销随表格规模增长：比值 %.2f 超过上限 10", ratio)
	}
}
