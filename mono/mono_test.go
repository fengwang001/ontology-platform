package mono

import (
	"errors"
	"testing"
)

type shapeCase struct {
	name string
	seq  []int
}

func shapes(n int) []shapeCase {
	eq, inc, dec := make([]int, n), make([]int, n), make([]int, n)
	for i := range eq {
		eq[i], inc[i], dec[i] = 7, i, n-i
	}
	return []shapeCase{{"全相等", eq}, {"单调递增", inc}, {"单调递减", dec}}
}

func TestOpsLinear(t *testing.T) {
	const w = 64
	for _, n := range []int{1000, 100000} {
		for _, sh := range shapes(n) {
			t.Run(sh.name, func(t *testing.T) {
				var q Queue
				for i, v := range sh.seq {
					if err := q.Push(i, v); err != nil {
						t.Fatal(err)
					}
					q.Evict(i - w + 1)
				}
				if q.ops > 2*n {
					t.Fatalf("n=%d: 入出队总次数 %d 超过 2n=%d", n, q.ops, 2*n)
				}
				if err := q.Verify(n-w, n-1, w); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestPushRejectsNonIncreasing(t *testing.T) {
	cases := []struct {
		name string
		idx  []int // 最后一个下标非法
	}{
		{"重复下标", []int{0, 1, 1}},
		{"下标回退", []int{0, 2, 1}},
		{"首元素后重复", []int{3, 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var q Queue
			for _, i := range c.idx[:len(c.idx)-1] {
				if err := q.Push(i, i+10); err != nil {
					t.Fatal(err)
				}
			}
			ops, pushed := q.ops, q.pushed
			before, _ := q.Max()
			if err := q.Push(c.idx[len(c.idx)-1], 99); !errors.Is(err, ErrBadIndex) {
				t.Fatalf("期望 ErrBadIndex，得到 %v", err)
			}
			if q.ops != ops || q.pushed != pushed {
				t.Fatal("被拒的 Push 改动了计数器")
			}
			if after, _ := q.Max(); after != before {
				t.Fatal("被拒的 Push 改动了队列内容")
			}
			if err := q.Push(100, 5); err != nil { // 被拒后仍可正常使用
				t.Fatal(err)
			}
		})
	}
}
