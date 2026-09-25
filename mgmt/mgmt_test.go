package mgmt

import (
	"sync"
	"testing"
)

// 并发：N 个 goroutine 各自 Snapshot 同一页再写不同偏移，全部结束后
// 各 view 互不影响（自己的写 + 共享页旧值），引用计数守恒、无泄漏。
func TestConcurrentSnapshotWrite(t *testing.T) {
	const n = 64
	mgr := New(n, n+1)
	v0, err := mgr.Alloc(make([]byte, n))
	if err != nil {
		t.Fatal(err)
	}
	views := make([]View, n)
	var wg sync.WaitGroup
	for i := range views {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _ := mgr.Snapshot(v0) // v0 存活，Snapshot 不会失败
			views[i] = v
			_ = mgr.Write(v, i, byte(i+1))
		}(i)
	}
	wg.Wait()
	for i, v := range views {
		exp := make([]byte, n)
		exp[i] = byte(i + 1)
		for off := 0; off < n; off++ {
			got, _ := mgr.Read(v, off)
			if got != exp[off] {
				t.Fatalf("view %d off %d: got %d want %d", i, off, got, exp[off])
			}
		}
		if err := mgr.Release(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.Release(v0); err != nil {
		t.Fatal(err)
	}
	if mgr.PageCount() != 0 {
		t.Fatalf("leak: %d pages left", mgr.PageCount())
	}
}

// 复杂度约束：m 个 view 共享同一页时，一次 Write 检查的引用记录数
// 不随 m 增长（每页一条引用计数，O(1) 判定共享，而非逐个 view 扫描）。
// 白盒测试：直接读非导出字段 wrChecks，公开接口不暴露它。
func TestWriteChecksConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		mgr := New(4, m+2)
		v0, err := mgr.Alloc([]byte{0, 0, 0, 0})
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < m; i++ {
			if _, err := mgr.Snapshot(v0); err != nil {
				t.Fatalf("m=%d snapshot %d: %v", m, i, err)
			}
		}
		if err := mgr.Write(v0, 0, 1); err != nil { // 共享→拷贝路径
			t.Fatalf("m=%d write: %v", m, err)
		}
		if mgr.wrChecks > 1 {
			t.Fatalf("m=%d: write checked %d ref records, want <= 1", m, mgr.wrChecks)
		}
		if err := mgr.Write(v0, 1, 2); err != nil { // 独占→原地写路径
			t.Fatalf("m=%d exclusive write: %v", m, err)
		}
		if mgr.wrChecks > 1 {
			t.Fatalf("m=%d: exclusive write checked %d ref records", m, mgr.wrChecks)
		}
	}
}
