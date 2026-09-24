package ontology

import "testing"

// TestAddNodeMigrationBound 验证加入第 11 个节点时，
// 归属变化的 key 比例落在 [1/22, 3/22]（理论值约 1/11）。逐个 key 验证。
func TestAddNodeMigrationBound(t *testing.T) {
	keys := genKeys(numKeys)
	ids := nodeIDs(10)

	r := New()
	for _, id := range ids {
		if err := r.Add(id, 200); err != nil {
			t.Fatalf("Add(%q): %v", id, err)
		}
	}
	before := locateAll(t, r, keys)

	if err := r.Add("node-10", 200); err != nil {
		t.Fatalf("Add(node-10): %v", err)
	}
	after := locateAll(t, r, keys)

	moved := 0
	for i := range keys {
		if before[i] != after[i] {
			moved++
		}
	}
	ratio := float64(moved) / float64(numKeys)
	t.Logf("搬迁比例: %d/%d = %.4f（理论值约 1/11 ≈ %.4f）", moved, numKeys, ratio, 1.0/11)
	if ratio < 1.0/22 || ratio > 3.0/22 {
		t.Fatalf("搬迁比例 %.4f 不在 [1/22, 3/22] 内", ratio)
	}
}

// TestRemoveNodeMigrationExact 验证删除一个节点时，
// 只有原本归属该节点的 key 改变归属，其余 key 逐个不变。
func TestRemoveNodeMigrationExact(t *testing.T) {
	keys := genKeys(numKeys)

	r := New()
	for _, id := range nodeIDs(11) {
		if err := r.Add(id, 200); err != nil {
			t.Fatalf("Add(%q): %v", id, err)
		}
	}
	before := locateAll(t, r, keys)

	const victim = "node-05"
	if err := r.Remove(victim); err != nil {
		t.Fatalf("Remove(%q): %v", victim, err)
	}
	after := locateAll(t, r, keys)

	moved := 0
	for i, k := range keys {
		if before[i] == victim {
			// 原属被删节点的 key 必须搬到别的节点。
			if after[i] == victim {
				t.Fatalf("key %q 仍归属已删除节点 %q", k, victim)
			}
			moved++
			continue
		}
		// 一致性哈希定义性质：其余 key 归属逐个不变。
		if after[i] != before[i] {
			t.Fatalf("key %q 归属从 %q 变为 %q，但它不属于被删节点 %q",
				k, before[i], after[i], victim)
		}
	}
	t.Logf("被删节点 %q 原有 %d 个 key 全部搬迁，其余 %d 个 key 归属不变",
		victim, moved, numKeys-moved)
	if moved == 0 {
		t.Fatal("被删节点原本没有任何 key，测试无效")
	}
}
