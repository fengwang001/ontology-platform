package store

import "testing"

// TestApplyIsTailAppend 证明 Apply 是纯尾部追加：对同一 Key 先追加 m 条
// delta，再 Apply 一条新 delta，断言本次访问的已有条目数不随 m 增长。
// 计数器 lastApplyVisits 是非导出字段，此处为包内测试直接读取，
// 不经过任何导出的函数或方法。
func TestApplyIsTailAppend(t *testing.T) {
	const maxVisits = 1 // 与 m 无关的小常数上界
	for _, m := range []int{100, 1000, 5000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			if s.Apply("k", int64(i), m+1) {
				t.Fatalf("m=%d: 第 %d 条不应溢出", m, i)
			}
		}
		if s.Apply("k", 1, m+1) {
			t.Fatalf("m=%d: 最后一条不应溢出", m)
		}
		if s.lastApplyVisits > maxVisits {
			t.Fatalf("m=%d: Apply 访问了 %d 个已有条目，随 m 增长", m, s.lastApplyVisits)
		}
		if got := s.DeltaCount(); got != m+1 {
			t.Fatalf("m=%d: DeltaCount=%d， want %d", m, got, m+1)
		}
	}
}

// TestStoreTableDriven 表驱动：追加/合并/读取的基本语义。
func TestStoreTableDriven(t *testing.T) {
	cases := []struct {
		name  string
		ops   []int64 // 依次对同一 Key 追加
		want  int64
		merge bool // 追加后是否先 Compact
	}{
		{"空日志", nil, 0, false},
		{"单正", []int64{10}, 10, false},
		{"正负混合", []int64{10, -4}, 6, false},
		{"零 delta", []int64{0, 0}, 0, false},
		{"合并后保值", []int64{10, -4}, 6, true},
		{"全负合并", []int64{-3, -7}, -10, true},
	}
	for _, c := range cases {
		s := New()
		for _, d := range c.ops {
			s.Apply("k", d, len(c.ops)+1)
		}
		if c.merge {
			s.Compact()
			if n := s.DeltaCount(); n != 0 {
				t.Fatalf("%s: Compact 后 DeltaCount=%d", c.name, n)
			}
		}
		if got := s.Get("k"); got != c.want {
			t.Fatalf("%s: Get=%d， want %d", c.name, got, c.want)
		}
	}
	if got := New().Get("不存在"); got != 0 {
		t.Fatalf("未知 Key 应得 0， got %d", got)
	}
}

// TestOverflowLeavesNoTrace 容量超限被拒且状态不变。
func TestOverflowLeavesNoTrace(t *testing.T) {
	s := New()
	s.Apply("a", 5, 1)
	if !s.Apply("b", 1, 1) {
		t.Fatal("第二条应被判溢出")
	}
	if got := s.Get("b"); got != 0 {
		t.Fatalf("被拒的 Apply 留下了痕迹: Get(b)=%d", got)
	}
	if n := s.DeltaCount(); n != 1 {
		t.Fatalf("DeltaCount=%d， want 1", n)
	}
	if got := s.Get("a"); got != 5 {
		t.Fatalf("Get(a)=%d， want 5", got)
	}
}
