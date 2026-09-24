package chq

import "testing"

// 大 m 下入队/出队/通道状态追加的访问个数不随 m 增长（白盒：直接读非导出计数器）。
func TestVisitsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c := &Ch{}
		for i := 0; i < m; i++ {
			c.Arrive(i)
		}
		c.SnapshotState() // 通道状态累积 m 条
		c.SetRecording(true)
		c.Arrive(1)
		c.StateAppend(1) // 模拟 uck 记录中到达
		if c.visits > 4 {
			t.Fatalf("m=%d Arrive visits=%d", m, c.visits)
		}
		c.Step()
		if c.visits > 4 {
			t.Fatalf("m=%d Step visits=%d", m, c.visits)
		}
	}
}

func TestFIFO(t *testing.T) {
	c := &Ch{}
	for i := 0; i < 10; i++ {
		c.Arrive(i)
	}
	for i := 0; i < 10; i++ {
		if v, ok := c.Step(); !ok || v != i {
			t.Fatalf("step %d: got %d %v", i, v, ok)
		}
	}
	if _, ok := c.Step(); ok {
		t.Fatal("step on empty queue succeeded")
	}
}
