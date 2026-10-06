package docsync

import (
	"strings"
	"testing"
)

// TestPerfEditTouchCount 证明小编辑不会逐行访问编辑点之前的数据：
// 在 N 行大文档末尾做一个小编辑，rope 触达节点数应只随 log(N) 缓慢增长，
// 而朴素整串重建是 O(N)。比较不同规模的访问量增长斜率。
func TestPerfEditTouchCount(t *testing.T) {
	sizes := []int{2000, 8000, 32000}
	for _, n := range sizes {
		doc := strings.Repeat("line of text content here\n", n)
		s := NewService(doc)
		// 定位最后一行开头（文档末尾换行之后为空末行，改为改倒数第二行行尾）。
		resetTouches()
		// 在文档中部偏后做一个 1 码元插入，取一个具体行/列位置。
		line := n - 1
		r := s.Change(0, []Edit{{
			Range: Range{Start: Position{line, 0}, End: Position{line, 0}},
			Text:  "Z",
		}})
		got := touches.Load()
		if !r.Accepted {
			t.Fatalf("edit rejected: %v", r.Reason)
		}
		t.Logf("N=%6d lines rope touches for tail insert = %d", n, got)
		// 即使文档扩大 16 倍，小编辑触达节点数也应保持在小的对数级上界内，
		// 而不是随总行数线性增长。上界对 treap 高度与 race 插桩都留有余量。
		bound := uint64(200 + 12*bitLenU(n))
		if got > bound {
			t.Errorf("N=%d touches=%d exceeds sublinear bound %d (likely linear)", n, got, bound)
		}
	}
}

// TestPerfDiagTouchCount 证明小编辑/迁移不逐个改动编辑点之前的诊断：
// 在大量诊断之后做一个尾部小替换，诊断树触达节点数应保持有界（与诊断总数无关）。
func TestPerfDiagTouchCount(t *testing.T) {
	counts := []int{500, 2000, 8000}
	for _, d := range counts {
		// 一行 ASCII 文本，长度 d+2，把诊断放在前 d 个不同位置。
		s := NewService(strings.Repeat("a", d+2))
		for i := 0; i < d; i++ {
			if _, err := s.Register(0, Diagnostic{
				Range: Range{Start: Position{0, i}, End: Position{0, i + 1}},
			}); err != nil {
				t.Fatal(err)
			}
		}
		resetTouches()
		// 在最末尾插入 1 码元：所有诊断都在插入点之前，理想情况下几乎不触达。
		r := s.Change(0, []Edit{{
			Range: Range{Start: Position{0, d + 2}, End: Position{0, d + 2}},
			Text:  "Z",
		}})
		got := touches.Load()
		if !r.Accepted {
			t.Fatal(r.Reason)
		}
		t.Logf("D=%5d diagnostics, tail insert touches = %d", d, got)
		// 尾部插入不触达编辑点之前的诊断；访问量与诊断总数无关（仅分裂路径）。
		// 用对 race 插桩稳健的宽松对数上界断言亚线性。
		bound := uint64(80 + 4*bitLenU(d))
		if got > bound {
			t.Errorf("D=%d touches=%d exceeds sublinear bound %d (likely linear)", d, got, bound)
		}
	}
}

func bitLenU(x int) int {
	n := 0
	for x > 0 {
		x >>= 1
		n++
	}
	return n
}

// TestPerfPositionOffsetSublinear 位置↔偏移换算不随行数线性增长。
func TestPerfPositionOffsetSublinear(t *testing.T) {
	sizes := []int{2000, 8000, 32000}
	for _, n := range sizes {
		doc := strings.Repeat("x\n", n)
		s := NewService(doc)
		resetTouches()
		// 取靠后某一行的位置 → 偏移。
		if _, err := s.PositionToOffset(Position{n - 1, 0}); err != nil {
			t.Fatal(err)
		}
		got := touches.Load()
		t.Logf("N=%6d lines, position->offset touches = %d", n, got)
		bound := uint64(80 + 6*bitLenU(n))
		if got > bound {
			t.Errorf("N=%d position touches=%d exceeds bound %d", n, got, bound)
		}
	}
}

// TestPerfNaiveIsLinear 对照：朴素模型整篇重建的成本随规模线性增长。
// 这里通过其码元数组拷贝长度间接佐证 O(N)，与 treap 的亚线性形成对比。
func TestPerfNaiveIsLinear(t *testing.T) {
	for _, n := range []int{1000, 4000} {
		doc := strings.Repeat("a", n)
		m := newNaive(doc)
		// 末尾插入：朴素实现重建整个码元切片（新切片长度随 N 线性）。
		m.change(0, []Edit{{
			Range: Range{Start: Position{0, n}, End: Position{0, n}},
			Text:  "Z",
		}})
		if len(m.cu) != n+1 {
			t.Fatalf("naive length=%d", len(m.cu))
		}
		t.Logf("naive rebuilt full UTF-16 slice of size %d (linear work)", n+1)
	}
}
