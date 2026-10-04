package envlock

import (
	"reflect"
	"testing"
)

func TestLockFIFO(t *testing.T) {
	var lock Lock
	if lock.Running() != 0 {
		t.Fatalf("new lock running = %d, want 0", lock.Running())
	}
	if _, ok := lock.Peek(); ok {
		t.Fatal("new lock queue not empty")
	}
	for _, id := range []int64{3, 1, 2} {
		lock.Enqueue(id)
	}
	if got := lock.Queue(); !reflect.DeepEqual(got, []int64{3, 1, 2}) {
		t.Fatalf("queue = %v, want [3 1 2]", got)
	}
	if !lock.Remove(1) {
		t.Fatal("Remove(1) = false, want true")
	}
	if lock.Remove(1) {
		t.Fatal("second Remove(1) = true, want false")
	}
	head, ok := lock.Peek()
	if !ok || head != 3 {
		t.Fatalf("Peek = %d,%v, want 3,true", head, ok)
	}
	lock.Acquire(head)
	if lock.Running() != 3 {
		t.Fatalf("Running = %d, want 3", lock.Running())
	}
	if got := lock.Queue(); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("queue = %v, want [2]", got)
	}
	lock.Release()
	if lock.Running() != 0 {
		t.Fatalf("Running = %d, want 0", lock.Running())
	}
	lock.Pop()
	if _, ok := lock.Peek(); ok {
		t.Fatal("queue not empty after pops")
	}
	lock.Pop() // popping an empty queue must not panic
}

func TestLockDequeueOrder(t *testing.T) {
	cases := [][]int64{
		{1},
		{1, 2, 3, 4, 5},
		{9, 7, 5, 3, 1},
	}
	for _, ids := range cases {
		var lock Lock
		for _, id := range ids {
			lock.Enqueue(id)
		}
		var got []int64
		for {
			head, ok := lock.Peek()
			if !ok {
				break
			}
			got = append(got, head)
			lock.Pop()
		}
		if !reflect.DeepEqual(got, ids) {
			t.Fatalf("dequeue order = %v, want %v", got, ids)
		}
	}
}
