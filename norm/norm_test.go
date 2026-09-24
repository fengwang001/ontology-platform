package norm

import "testing"

// TestTailCheckConstant 证明折叠只检查序列末尾一条：
// 未了结序列长度 m 取多档，喂入一个不与末尾抵消的操作后，
// 最近一次 Apply 的末尾检查次数恒为小常数（<=1），不随 m 增长。
// 计数器 checks 是非导出字段，仅本白盒测试可读。
func TestTailCheckConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		n := New(m + 1)
		for i := 0; i < m; i++ {
			if err := n.Apply(1); err != nil { // 同号互不抵消，序列长度到 m
				t.Fatalf("m=%d 第 %d 步意外失败: %v", m, i, err)
			}
		}
		if err := n.Apply(-2); err != nil { // 量值不等，不与末尾 +1 抵消
			t.Fatalf("m=%d 探测操作意外失败: %v", m, err)
		}
		if n.checks > 1 {
			t.Fatalf("m=%d 检查次数 %d，随规模增长，疑似整条扫描", m, n.checks)
		}
		if got := len(n.Changelog()); got != m+1 {
			t.Fatalf("m=%d 序列长度=%d，期望 %d", m, got, m+1)
		}
	}
}

// TestCancelAlsoChecksOnce 抵消路径同样只查末尾一次。
func TestCancelAlsoChecksOnce(t *testing.T) {
	n := New(4)
	for _, op := range []Op{3, 5, 5, -5} {
		if err := n.Apply(op); err != nil {
			t.Fatalf("意外失败: %v", err)
		}
	}
	if n.checks != 1 {
		t.Fatalf("抵消路径检查次数=%d，期望 1", n.checks)
	}
	if got := n.Changelog(); len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Fatalf("抵消后序列=%v，期望 [3 5]", got)
	}
}
