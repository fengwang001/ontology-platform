package chq

import "testing"

// TestAccessBound 证明 Push/Pop 访问的已存记录个数不随队列与通道状态规模 m 增长：
// 队列已缓冲 m 条、通道状态已累积 m 条后，单次 Push/Pop 访问个数不超过小常数。
func TestAccessBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c := New()
		for i := 0; i < m; i++ {
			c.Push(i)
		}
		c.Begin()   // 通道状态 = 当前队列 m 条
		c.Barrier() // 停止记录，通道状态保持 m 条
		c.Push(-1)  // 队尾追加一条
		if c.access > 4 {
			t.Fatalf("m=%d: Push 访问 %d 条已存记录，随 m 增长", m, c.access)
		}
		if _, ok := c.Pop(); !ok {
			t.Fatalf("m=%d: Pop 应成功", m)
		}
		if c.access > 4 {
			t.Fatalf("m=%d: Pop 访问 %d 条已存记录，随 m 增长", m, c.access)
		}
	}
}

// TestFIFOAndRestore 表驱动核验 FIFO、记录中累积、屏障后留存与恢复重建。
func TestFIFOAndRestore(t *testing.T) {
	cases := []struct {
		name string
		ops  []int // 正数=Push；0=Pop；-1=Begin；-2=Barrier；-3=Commit；-4=Restore
		want []int // 最终依次 Pop 全部应得到的值
	}{
		{"纯FIFO", []int{1, 2, 3, 0, 4}, []int{2, 3, 4}},
		{"记录中累积", []int{5, -1, 6, -2, 7, -3, -4}, []int{5, 6, 7}},
		{"快照后处理仍留通道状态", []int{8, -1, 0, -2, -3, -4}, []int{8}},
		{"无检查点恢复为全部到达", []int{1, 0, 2, -4}, []int{1, 2}},
	}
	for _, tc := range cases {
		c := New()
		for _, o := range tc.ops {
			switch o {
			case 0:
				c.Pop()
			case -1:
				c.Begin()
			case -2:
				c.Barrier()
			case -3:
				c.Commit()
			case -4:
				c.Restore()
			default:
				c.Push(o)
			}
		}
		for i, w := range tc.want {
			if v, ok := c.Pop(); !ok || v != w {
				t.Fatalf("%s: 第%d条=%v,%v，期望 %d", tc.name, i, v, ok, w)
			}
		}
		if _, ok := c.Pop(); ok {
			t.Fatalf("%s: 队列应为空", tc.name)
		}
	}
}
