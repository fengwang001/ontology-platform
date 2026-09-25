package q

import "testing"

// 钉住不变量 3 的 O(1) 半边：出队只碰头节点，访问节点数不随 m 增长。
func TestDequeueVisitsOneNode(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		qu := New(m)
		for i := 0; i < m; i++ {
			if err := qu.Enqueue(i); err != nil {
				t.Fatalf("m=%d enqueue %d: %v", m, i, err)
			}
		}
		if _, ok := qu.Dequeue(); !ok {
			t.Fatalf("m=%d dequeue failed", m)
		}
		if got := qu.lastVisited.Load(); got != 1 {
			t.Fatalf("m=%d: dequeue visited %d nodes, want 1", m, got)
		}
	}
	// 空队列出队同样只碰哨兵一个节点
	qu := New(1)
	if _, ok := qu.Dequeue(); ok {
		t.Fatal("empty dequeue should be (0,false)")
	}
	if got := qu.lastVisited.Load(); got != 1 {
		t.Fatalf("empty dequeue visited %d nodes, want 1", got)
	}
}

// 包内顺序冒烟：FIFO 与 Len 守恒。
func TestSequentialFIFO(t *testing.T) {
	qu := New(8)
	for i := 0; i < 8; i++ {
		if err := qu.Enqueue(i); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		if qu.Len() != i+1 {
			t.Fatalf("len after enqueue %d = %d", i, qu.Len())
		}
	}
	if err := qu.Enqueue(8); err != ErrFull {
		t.Fatalf("full enqueue err = %v, want ErrFull", err)
	}
	for i := 0; i < 8; i++ {
		v, ok := qu.Dequeue()
		if !ok || v != i {
			t.Fatalf("dequeue %d = (%d,%v)", i, v, ok)
		}
	}
	if v, ok := qu.Dequeue(); ok || v != 0 {
		t.Fatalf("drained dequeue = (%d,%v), want (0,false)", v, ok)
	}
}
