package deadline

import (
	"errors"
	"testing"
)

func TestHeapOrderAndTieBreak(t *testing.T) {
	h := NewHeap()
	h.Set(S2S, 10)
	h.Set(HB, 5)
	h.Set(S2C, 5)
	h.Set(SC, 5)
	// 同刻 5：sc、s2c、hb；之后 s2s 在 10。
	want := []Kind{SC, S2C, HB, S2S}
	for i, w := range want {
		top, ok := h.Pop()
		if !ok || top.Kind != w {
			t.Fatalf("pop %d: got %v ok=%v want %v", i, top, ok, w)
		}
	}
	if h.Len() != 0 {
		t.Fatalf("heap not empty: %d", h.Len())
	}
}

func TestHeapUpdateAndRemove(t *testing.T) {
	h := NewHeap()
	h.Set(HB, 10)
	h.Set(S2C, 8)
	h.Set(HB, 3) // 更新为更早
	top, _ := h.Peek()
	if top.Kind != HB || top.At != 3 {
		t.Fatalf("unexpected top %+v", top)
	}
	h.Set(HB, -1) // 移除
	top, _ = h.Peek()
	if top.Kind != S2C {
		t.Fatalf("unexpected top after remove %+v", top)
	}
	cp := h.Clone()
	h.Pop()
	if cp.Len() != 1 {
		t.Fatalf("clone must be independent, len=%d", cp.Len())
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{S2S: 1, S2C: 10, HB: 4, SC: 100}).Validate(); err != nil {
		t.Fatal(err)
	}
	// s2c 与 sc 至少一个非零。
	if !errors.Is((Config{S2S: 1}).Validate(), ErrConfig) {
		t.Fatal("want ErrConfig when s2c=sc=0")
	}
	if !errors.Is((Config{S2C: 1_000_000_001}).Validate(), ErrConfig) {
		t.Fatal("want ErrConfig for value > 1e9")
	}
	// 仅 sc 非零合法（运行期无 s2c 项）。
	if err := (Config{SC: 5}).Validate(); err != nil {
		t.Fatal(err)
	}
}
