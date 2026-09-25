package hlist

import "testing"

// TestUnlinkIsConstant 证明物理摘除是 O(1) 局部操作：
// 先 mark 之后的摘除阶段只访问前驱+后继（≤2）个节点，不随 m 增长。
// 直接读同包非导出字段 unlinkVisited，不经任何导出接口。
func TestUnlinkIsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l := New()
		for i := 0; i < m; i++ {
			if err := l.Insert(i); err != nil {
				t.Fatalf("m=%d insert %d: %v", m, i, err)
			}
		}
		if err := l.Delete(m / 2); err != nil {
			t.Fatalf("m=%d delete: %v", m, err)
		}
		if got := l.unlinkVisited.Load(); got > 2 {
			t.Fatalf("m=%d 摘除访问了 %d 个节点，超过常数 2", m, got)
		}
		if l.Contains(m/2) || len(l.List()) != m-1 {
			t.Fatalf("m=%d 删除后内容不正确", m)
		}
	}
}
