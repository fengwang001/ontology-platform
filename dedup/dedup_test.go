package dedup

import "testing"

// TestCheckCountConstant 证明查重按 txid 直接哈希定位：
// 集合大小 m 取多档，全新 txid 的 Seen/Add 检查个数不随 m 线性增长。
func TestCheckCountConstant(t *testing.T) {
	const limit = 2 // 与 m 无关的小常数
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			s.Add(int64(i + 1))
		}
		fresh := int64(m + 1)
		if s.Seen(fresh) {
			t.Fatalf("m=%d: fresh txid %d 不应已存在", m, fresh)
		}
		if s.checked > limit {
			t.Fatalf("m=%d: Seen 检查了 %d 个 txid，超过常数 %d（疑似线性扫描）", m, s.checked, limit)
		}
		s.Add(fresh)
		if s.checked > limit {
			t.Fatalf("m=%d: Add 检查了 %d 个 txid，超过常数 %d（疑似线性扫描）", m, s.checked, limit)
		}
		if !s.Seen(fresh) {
			t.Fatalf("m=%d: Add 后 Seen(%d) 应为真", m, fresh)
		}
	}
}

// TestSnapshotSorted 钉住升序快照与精确集合语义。
func TestSnapshotSorted(t *testing.T) {
	s := New()
	for _, tx := range []int64{9, 3, 7, 1} {
		s.Add(tx)
	}
	s.Add(3) // 重复加入不改变集合
	got := s.Snapshot()
	want := []int64{1, 3, 7, 9}
	if len(got) != len(want) {
		t.Fatalf("Snapshot 长度 = %d，want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Snapshot[%d] = %d，want %d", i, got[i], want[i])
		}
	}
}
