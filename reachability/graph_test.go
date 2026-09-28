package reachability

import "testing"

// TestSkeleton 仅用于在骨架阶段确认测试可编译运行；后续用例会替换它。
func TestSkeleton(t *testing.T) {
	g := New()
	if g == nil {
		t.Fatal("New returned nil")
	}
}
