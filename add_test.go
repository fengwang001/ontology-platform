package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestAddSpecialValues(t *testing.T) {
	h, err := NewHistogram(0, 1, 3)
	if err != nil {
		t.Fatalf("NewHistogram: %v", err)
	}

	// NaN 被拒绝、计入 skipped、返回 ErrNaN，不进桶也不进溢出。
	if err := h.Add(math.NaN()); !errors.Is(err, ErrNaN) {
		t.Fatalf("Add(NaN): want ErrNaN, got %v", err)
	}
	if got := h.Skipped(); got != 1 {
		t.Fatalf("Skipped: want 1, got %d", got)
	}
	if h.Overflow() != 0 || h.Underflow() != 0 {
		t.Fatalf("NaN 不应进溢出: under=%d over=%d", h.Underflow(), h.Overflow())
	}
	if sum(h.Buckets()) != 0 {
		t.Fatalf("NaN 不应进任何桶: %v", h.Buckets())
	}

	// -Inf 下溢，+Inf 上溢。
	if err := h.Add(math.Inf(-1)); err != nil {
		t.Fatalf("Add(-Inf): %v", err)
	}
	if err := h.Add(math.Inf(1)); err != nil {
		t.Fatalf("Add(+Inf): %v", err)
	}
	if h.Underflow() != 1 || h.Overflow() != 1 {
		t.Fatalf("Inf 计数错误: under=%d over=%d", h.Underflow(), h.Overflow())
	}

	// +0.0 与 -0.0 在 lo=0 时都进第 0 桶。
	if err := h.Add(0); err != nil {
		t.Fatalf("Add(+0): %v", err)
	}
	if err := h.Add(math.Copysign(0, -1)); err != nil {
		t.Fatalf("Add(-0): %v", err)
	}
	b := h.Buckets()
	if b[0] != 2 {
		t.Fatalf("±0 都应进第 0 桶: %v", b)
	}

	// hi 上溢，前一个可表示数进最后一桶。
	if err := h.Add(1); err != nil {
		t.Fatalf("Add(hi): %v", err)
	}
	if err := h.Add(math.Nextafter(1, math.Inf(-1))); err != nil {
		t.Fatalf("Add(pred(hi)): %v", err)
	}
	if h.Overflow() != 2 { // +Inf 与 hi
		t.Fatalf("Overflow: want 2, got %d", h.Overflow())
	}
	if h.Buckets()[2] != 1 {
		t.Fatalf("pred(hi) 应进最后一桶: %v", h.Buckets())
	}
}

func TestBucketsReturnsCopy(t *testing.T) {
	h, _ := NewHistogram(0, 1, 3)
	_ = h.Add(0.1)
	got := h.Buckets()
	got[0] = 999
	if h.Buckets()[0] != 1 {
		t.Fatalf("Buckets() 未返回拷贝：内部状态被调用方改动")
	}
}

func sum(xs []uint64) uint64 {
	var s uint64
	for _, x := range xs {
		s += x
	}
	return s
}
