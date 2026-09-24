package segment

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// TestCheckedBound 检查个数（索引条目检查数+扫描记录数）不随 m 线性增长：
// 先二分定位 floor 条目，再做有界的段内顺序扫描。
func TestCheckedBound(t *testing.T) {
	const k, size = 4, 10
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s, err := New(0, k*size)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if _, err := s.Append(int64(i), size); err != nil {
				t.Fatal(err)
			}
		}
		rng := rand.New(rand.NewSource(int64(m)))
		bound := int64(2*ceilLog2(m+1) + k + 4)
		for j := 0; j < 200; j++ {
			if _, _, _, err := s.Lookup(int64(rng.Intn(m + 1))); err != nil && !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if got := s.checked.Load(); got > bound {
				t.Fatalf("m=%d: checked %d > bound %d", m, got, bound)
			}
		}
	}
}

func TestFailureNoTrace(t *testing.T) {
	s, _ := New(100, 10)
	for i := 0; i < 50; i++ {
		if _, err := s.Append(100+int64(i)*2, 7); err != nil {
			t.Fatal(err)
		}
	}
	last := int64(100 + 49*2)
	before := fmt.Sprint(s.Records(), s.Entries())
	bad := []func() error{
		func() error { _, e := s.Append(last, 5); return e },      // 位点不严格递增
		func() error { _, e := s.Append(last+1, 0); return e },    // size 非法
		func() error { _, e := s.Append(99, 5); return e },        // 低于基位点
		func() error { _, e := s.Append(100+1<<31, 5); return e }, // 相对位点溢出
		func() error { _, _, _, e := s.Lookup(99); return e },     // 查找低于基位点
		func() error { _, e := New(-1, 10); return e },            // base 非法
		func() error { _, e := New(0, 0); return e },              // interval 非法
	}
	for i, op := range bad {
		if op() == nil || fmt.Sprint(s.Records(), s.Entries()) != before {
			t.Fatalf("op %d: rejected op left trace", i)
		}
	}
	if _, err := s.Append(last+2, 5); err != nil {
		t.Fatalf("segment unusable after rejections: %v", err)
	}
}

func TestErrorsDistinct(t *testing.T) {
	s, _ := New(100, 10)
	s.Append(100, 5)
	_, e1 := s.Append(100, 5)       // 参数或追加非法
	_, e2 := s.Append(100+1<<31, 5) // 相对位点溢出
	_, _, _, e3 := s.Lookup(99)     // 查找低于基位点
	_, _, _, e4 := s.Lookup(101)    // 未找到
	sents := []error{ErrInvalid, ErrRelOverflow, ErrBelowBase, ErrNotFound}
	for i, e := range []error{e1, e2, e3, e4} {
		for j, w := range sents {
			if errors.Is(e, w) != (i == j) {
				t.Fatalf("err %d vs sentinel %d mismatch", i, j)
			}
		}
	}
}

func ceilLog2(n int) int {
	c := 0
	for (1 << c) < n {
		c++
	}
	return c
}
