package registry

import "testing"

// TestRegistry 表驱动覆盖注册表原语与桶数计数。
func TestRegistry(t *testing.T) {
	r := New(2)
	if r.MaxB() != 2 {
		t.Errorf("MaxB=%d, want 2", r.MaxB())
	}
	if r.Has("b1") {
		t.Error("空注册表不应有 b1")
	}
	r.Create("A", "b1")
	r.Create("A", "b2")
	r.Create("B", "b3")
	if got := r.Count("A"); got != 2 {
		t.Errorf("Count(A)=%d, want 2", got)
	}
	r.AddBytes("b1", 40)
	r.AddBytes("b1", -10)
	r.IncOpen("b1")
	r.IncOpen("b1")
	r.DecOpen("b1")
	r.IncObjects("b1", 3)
	b, ok := r.Get("b1")
	if !ok || b.Owner != "A" || b.Bytes != 30 || b.Open != 1 || b.Objects != 3 {
		t.Errorf("Get(b1)=%+v,%v", b, ok)
	}
	// 转移只改属主与桶数，不动字节、上传数与对象记录。
	r.Transfer("b1", "B")
	b, _ = r.Get("b1")
	if b.Owner != "B" || b.Bytes != 30 || b.Open != 1 || b.Objects != 3 {
		t.Errorf("转移后 Get(b1)=%+v", b)
	}
	if got := r.Count("A"); got != 1 {
		t.Errorf("转移后 Count(A)=%d, want 1", got)
	}
	if got := r.Count("B"); got != 2 {
		t.Errorf("转移后 Count(B)=%d, want 2", got)
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("Get(nope) 应不存在")
	}
}
