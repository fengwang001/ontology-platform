package ontology

import "testing"

// 加入第 11 个节点后，归属变化的 key 比例必须落在 [1/22, 3/22]
// （理论值约 1/11），且所有变化的 key 必须迁往新节点。逐个 key 验证。
func TestAddNodeMigrationIsBounded(t *testing.T) {
	keys := GenerateKeys(100000)
	r := buildRing(t, 10, 200)
	before := ownersOf(t, r, keys)

	if err := r.Add(nodeID(10), 200); err != nil {
		t.Fatalf("Add: %v", err)
	}
	after := ownersOf(t, r, keys)

	changed := 0
	for i := range keys {
		if before[i] == after[i] {
			continue
		}
		changed++
		if after[i] != nodeID(10) {
			t.Fatalf("key %q moved %s -> %s, want new node %s",
				keys[i], before[i], after[i], nodeID(10))
		}
	}
	ratio := float64(changed) / float64(len(keys))
	if lo, hi := 1.0/22, 3.0/22; ratio < lo || ratio > hi {
		t.Fatalf("migration ratio %v out of [%v, %v] (theory ~1/11)", ratio, lo, hi)
	}
	t.Logf("migration ratio = %v (theory ~1/11)", ratio)
}

// 删除一个节点后，只有原本归属该节点的 key 允许改变归属，
// 其余 key 的归属必须逐个保持不变。这是一致性哈希的定义性质。
func TestRemoveNodeOnlyRelocatesItsKeys(t *testing.T) {
	keys := GenerateKeys(100000)
	r := buildRing(t, 10, 200)
	before := ownersOf(t, r, keys)

	const victim = "node-3"
	if err := r.Remove(victim); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	after := ownersOf(t, r, keys)

	moved := 0
	for i := range keys {
		if before[i] == victim {
			moved++
			if after[i] == victim {
				t.Fatalf("key %q still maps to removed node %q", keys[i], victim)
			}
			continue
		}
		if after[i] != before[i] {
			t.Fatalf("key %q changed owner %s -> %s though it was not on %s",
				keys[i], before[i], after[i], victim)
		}
	}
	if moved == 0 {
		t.Fatal("no key was owned by the removed node; test is vacuous")
	}
	t.Logf("relocated %d keys, %d untouched", moved, len(keys)-moved)
}
