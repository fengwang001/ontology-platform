package headers

import (
	"fmt"
	"slices"
	"testing"
)

// 白盒测试：读取非导出计数器 cmps，证明按名查找的比较数不随 N 线性增长。
func TestLookupComparisons(t *testing.T) {
	for _, n := range []int{50, 5000} {
		s := New(&Config{Width: 78}) // 零值 Limits：不限条数
		for i := 0; i < n; i++ {
			if err := s.Add(fmt.Sprintf("X-H%d", i), "v"); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add("Target", "v"); err != nil {
			t.Fatal(err)
		}
		before := s.cmps.Load()
		if got := s.GetAll("Target"); len(got) != 1 {
			t.Fatalf("N=%d: GetAll(Target) = %v", n, got)
		}
		delta := s.cmps.Load() - before
		if delta != 1 {
			t.Errorf("N=%d: 单次查找比较数 = %d, 应为 1（不随 N 增长）", n, delta)
		}
		// 不存在的名字：0 次比较
		before = s.cmps.Load()
		s.GetAll("Missing")
		if d := s.cmps.Load() - before; d != 0 {
			t.Errorf("N=%d: 查找不存在名字比较数 = %d, 应为 0", n, d)
		}
	}
}

// 索引更新（Del/Set 触发重建）后，保序语义不受影响。
func TestOrderSurvivesIndexRebuild(t *testing.T) {
	s := New(nil)
	for _, kv := range [][2]string{
		{"B", "1"}, {"A", "2"}, {"B", "3"}, {"C", "4"}, {"B", "5"},
	} {
		if err := s.Add(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	s.Del("A")             // 触发索引重建
	s.Set("C", "replaced") // 触发索引重建
	if err := s.Add("B", "6"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAll("B"); !slices.Equal(got, []string{"1", "3", "5", "6"}) {
		t.Errorf("索引重建后 GetAll(B) = %v, 保序失败", got)
	}
	want := "B: 1\r\nB: 3\r\nC: replaced\r\nB: 5\r\nB: 6\r\n\r\n"
	if string(s.Bytes()) != want {
		t.Errorf("索引重建后回写 = %q, want %q", s.Bytes(), want)
	}
}
