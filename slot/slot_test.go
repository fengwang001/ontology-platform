package slot

import (
	"fmt"
	"testing"
)

func TestFIFOAllocation(t *testing.T) {
	m := New(2)
	if m.Free() != 2 || m.Held() != 0 || m.Queued() != 0 {
		t.Fatalf("initial accounting wrong: free=%d held=%d q=%d", m.Free(), m.Held(), m.Queued())
	}

	e1 := m.Enqueue(1)
	e2 := m.Enqueue(2)
	e3 := m.Enqueue(3)
	if m.Front() != 1 || m.Queued() != 3 {
		t.Fatalf("queue order wrong: front=%d len=%d", m.Front(), m.Queued())
	}

	// Pop grants a slot and keeps FIFO order.
	if id := m.Pop(); id != 1 {
		t.Fatalf("pop %d", id)
	}
	if id := m.Pop(); id != 2 {
		t.Fatalf("pop %d", id)
	}
	if m.Held() != 2 || m.Free() != 0 || m.Queued() != 1 {
		t.Fatalf("after pops: held=%d free=%d q=%d", m.Held(), m.Free(), m.Queued())
	}

	// Removing the sole remaining element from the middle (here: only entry)
	// is O(1) and then the queue is empty.
	m.Remove(e3)
	_ = e1
	_ = e2
	if m.Queued() != 0 || m.Front() != 0 {
		t.Fatalf("queue after remove: len=%d front=%d", m.Queued(), m.Front())
	}

	m.Release()
	if m.Held() != 1 {
		t.Fatalf("release: held=%d", m.Held())
	}
}

func TestRemoveMiddleIsO1(t *testing.T) {
	m := New(1)
	m.Enqueue(10)
	mid := m.Enqueue(20)
	m.Enqueue(30)

	m.Remove(mid) // no scan; FIFO head/tail survive
	if ids := drainFronts(m); fmt.Sprint(ids) != "[10 30]" {
		t.Fatalf("after middle remove: %v", ids)
	}
}

func drainFronts(m *Manager) []int64 {
	var out []int64
	for m.Queued() > 0 {
		out = append(out, m.Front())
		m.Remove(m.PeekList().Front())
	}
	return out
}

func TestCanPopAndEmptyPop(t *testing.T) {
	m := New(1)
	if m.CanPop() {
		t.Fatal("empty queue cannot pop")
	}
	if id := m.Pop(); id != 0 {
		t.Fatalf("empty pop = %d", id)
	}
	m.Enqueue(99)
	if !m.CanPop() {
		t.Fatal("free slot + queued head must allow pop")
	}
	if id := m.Pop(); id != 99 {
		t.Fatalf("pop %d", id)
	}
	if m.CanPop() {
		t.Fatal("slot now held")
	}
}
