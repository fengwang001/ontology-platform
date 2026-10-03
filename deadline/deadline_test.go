package deadline

import (
	"errors"
	"testing"
)

func TestConfig(t *testing.T) {
	if _, err := NewConfig(0, 0, 4, 0); !errors.Is(err, ErrConfig) {
		t.Fatalf("s2c=sc=0: %v", err)
	}
	if _, err := NewConfig(-1, 10, 0, 5); !errors.Is(err, ErrConfig) {
		t.Fatalf("negative: %v", err)
	}
	if _, err := NewConfig(0, 10, 0, 1_000_000_001); !errors.Is(err, ErrConfig) {
		t.Fatalf("sc too big: %v", err)
	}
	c, err := NewConfig(2, 10, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := ScheduledItems(c, 0, 5); len(got) != 2 {
		t.Fatalf("scheduled items=%v", got)
	}
	r := RunningItems(c, 0, 1, 3)
	if len(r) != 3 || r[0].Kind != S2C || r[1].Kind != HB || r[2].Kind != SC {
		t.Fatalf("running items=%v", r)
	}
}

func TestHeapOrder(t *testing.T) {
	// 同刻顺序 SC < S2C < HB < S2S；不同时刻取最早。
	h := NewHeap(At{10, S2S}, At{10, HB}, At{10, S2C}, At{10, SC}, At{9, HB})
	want := []Kind{HB, SC, S2C, HB, S2S}
	for _, w := range want {
		top, ok := h.Pop()
		if !ok || top.Kind != w {
			t.Fatalf("pop=%v,%v want %v", top, ok, w)
		}
	}
	if h.Len() != 0 {
		t.Fatalf("len=%d", h.Len())
	}
	h.Push(At{5, Wake})
	if top, _ := h.Peek(); top.Kind != Wake {
		t.Fatalf("peek=%v", top)
	}
}

func TestWaitingItems(t *testing.T) {
	c, _ := NewConfig(0, 10, 0, 100)
	items := WaitingItems(c, 0, 12)
	h := NewHeap(items...)
	first, _ := h.Pop()
	if first.Kind != Wake || first.Time != 12 {
		t.Fatalf("first=%v", first)
	}
	// 同刻 SC 优先于 Wake。
	items = WaitingItems(c, 0, 100)
	h = NewHeap(items...)
	if first, _ = h.Pop(); first.Kind != SC || first.Time != 100 {
		t.Fatalf("tie first=%v", first)
	}
}
