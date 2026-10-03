package ontology

import "testing"

func TestNextDoesNotCompareTimerCount(t *testing.T) {
	sizes := []int{100, 10000}
	for _, size := range sizes {
		heap := newDeadlineHeap()
		for i := size - 1; i >= 0; i-- {
			heap.push(&timer{
				id:            "timer",
				next:          uint64(i),
				deadline:      uint64(i),
				deadlineIndex: -1,
				nextIndex:     -1,
				readyIndex:    -1,
			})
		}
		heap.resetCounters()
		got := heap.peek().deadline
		if got != 0 {
			t.Fatalf("size=%d root deadline=%d, want 0", size, got)
		}
		if heap.comparisons != 0 {
			t.Fatalf("size=%d Next comparisons=%d, want 0", size, heap.comparisons)
		}
	}
}

func TestWakeExaminationBound(t *testing.T) {
	c, err := NewCoalescer(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		mustAdd(t, c, "t"+string(rune('a'+i)), 100, uint64(9-i), 0)
	}
	c.nexts.resetCounters()
	c.ready.resetCounters()
	result, err := c.Wake()
	if err != nil || len(result.Fired) != 3 || result.Left != 7 {
		t.Fatalf("Wake() = (%+v,%v)", result, err)
	}
	if c.nexts.examined != 10 || c.ready.examined != 3 {
		t.Fatalf("examined nexts=%d ready=%d, want 10 and 3", c.nexts.examined, c.ready.examined)
	}
}
