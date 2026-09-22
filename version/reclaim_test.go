package version

import (
	"testing"

	"ontology/txid"
)

func TestReclaimKeepsAnchorAndOlderVisibleResult(t *testing.T) {
	c := NewChain(0)
	for i := 1; i <= 5; i++ {
		_ = c.Append(txid.TxID(i), []byte{byte('0' + i)}, false)
	}
	// barrier=4：保留链头 5（未被遮蔽），锚点为 3，回收 2、1。
	removed, examined := c.ReclaimBefore(4)
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if examined != 3 { // 走过 5,4,3 即找到锚点
		t.Fatalf("examined = %d, want 3", examined)
	}
	if c.Len() != 3 {
		t.Fatalf("len = %d, want 3", c.Len())
	}
	// 快照点 3（只看 <3）：1、2 已被回收，但锚点 3 对它本就不可见，
	// 可见结果应为“无”。这与回收前“1、2 被 3 遮蔽”的结果一致——
	// 真正的安全保证由 reclaim 选择 barrier 时保证（旧快照存在时 barrier 不会推进到这里）。
	if _, ok := c.Find(func(x txid.TxID) bool { return x < 3 }, 0); ok {
		t.Fatal("versions below anchor must be unreachable")
	}
	// 快照点 4：恰好看到锚点 3。
	got, ok := c.Find(func(x txid.TxID) bool { return x < 4 }, 0)
	if !ok || string(got.Value()) != "3" {
		t.Fatalf("anchor read got %q,%v", got.Value(), ok)
	}
	// 快照点很大：看到链头 5。
	got, ok = c.Find(func(x txid.TxID) bool { return x < 100 }, 0)
	if !ok || string(got.Value()) != "5" {
		t.Fatalf("head read got %q,%v", got.Value(), ok)
	}
}

func TestReclaimSingleVersionKept(t *testing.T) {
	c := NewChain(0)
	_ = c.Append(1, []byte("only"), false)
	removed, examined := c.ReclaimBefore(100)
	if removed != 0 || examined != 1 || c.Len() != 1 {
		t.Fatalf("got removed=%d examined=%d len=%d", removed, examined, c.Len())
	}
}

func TestReclaimDeleteAnchor(t *testing.T) {
	// 删除标记同样是“更新的可见版本”，可遮蔽更旧版本。
	c := NewChain(0)
	_ = c.Append(1, []byte("v1"), false)
	_ = c.Append(2, []byte("v2"), false)
	_ = c.Append(3, nil, true)
	removed, _ := c.ReclaimBefore(100)
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	got, ok := c.Find(func(x txid.TxID) bool { return x < 100 }, 0)
	if !ok || !got.Deleted() {
		t.Fatalf("expected delete marker, got %v,%v", got.Value(), ok)
	}
}
