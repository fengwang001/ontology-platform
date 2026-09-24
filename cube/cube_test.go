package cube

import (
	"fmt"
	"testing"

	"ontology/dim"
)

// 白盒：先构造 m 个互不相同的非空 cell，再加一个三维取值全新的事实，
// 断言本次触碰的 cell 键个数恒等于 8，与 m 无关（直接读非导出计数器）。
func TestTouchedIsConstant8(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprint("m=", m), func(t *testing.T) {
			c := New(1 << 20)
			for i := 0; len(c.sums) < m; i++ {
				if err := c.Add(fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i), fmt.Sprintf("c%d", i), 1); err != nil {
					t.Fatal(err)
				}
			}
			if len(c.sums) < m {
				t.Fatalf("setup: got %d cells, want >= %d", len(c.sums), m)
			}
			if err := c.Add("NA", "NB", "NC", 7); err != nil {
				t.Fatal(err)
			}
			if c.touched != 8 {
				t.Fatalf("add touched %d cells, want 8 (m=%d)", c.touched, m)
			}
			if err := c.Remove("NA", "NB", "NC", 7); err != nil {
				t.Fatal(err)
			}
			if c.touched != 8 {
				t.Fatalf("remove touched %d cells, want 8 (m=%d)", c.touched, m)
			}
		})
	}
}

// 白盒：Remove 一个不存在的整体事实（取值都在但组合没出现过）也被拒绝。
func TestRemoveUnknownComboRejected(t *testing.T) {
	c := New(1 << 20)
	if err := c.Add("a", "b", "c", 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("a", "b", "c", 3); err != ErrFactNotFound {
		t.Fatalf("got %v, want ErrFactNotFound", err)
	}
	if got := c.sums[dim.Cells("a", "b", "c")[7]]; got != 2 {
		t.Fatalf("state changed: got %d want 2", got)
	}
}
