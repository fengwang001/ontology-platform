package ontology

import (
	"math/rand"
	"testing"
)

// buildRing 按给定顺序把节点加入新环。
func buildRing(t *testing.T, ids []string, vnodes int) *Ring {
	t.Helper()
	r := New()
	for _, id := range ids {
		if err := r.Add(id, vnodes); err != nil {
			t.Fatalf("Add(%q): %v", id, err)
		}
	}
	return r
}

// positions 返回环上虚拟节点位置的快照（测试在同一包内，可直接访问）。
func positions(r *Ring) []uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]uint64(nil), r.sorted...)
}

// TestAddOrderIndependence 用 20 种不同添加顺序建环，
// 断言虚拟节点排布与每个 key 的归属逐个完全相同。
func TestAddOrderIndependence(t *testing.T) {
	ids := nodeIDs(8)
	keys := genKeys(20_000)

	base := buildRing(t, ids, 100)
	basePos := positions(base)
	baseOwners := locateAll(t, base, keys)

	// 固定种子的洗牌是确定性的，与 maphash 的随机种子无关。
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		perm := append([]string(nil), ids...)
		rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

		r := buildRing(t, perm, 100)
		gotPos := positions(r)
		if len(gotPos) != len(basePos) {
			t.Fatalf("排列 %v: 虚拟节点数 %d != %d", perm, len(gotPos), len(basePos))
		}
		for i := range basePos {
			if gotPos[i] != basePos[i] {
				t.Fatalf("排列 %v: 第 %d 个虚拟节点位置 %d != %d",
					perm, i, gotPos[i], basePos[i])
			}
		}
		gotOwners := locateAll(t, r, keys)
		for i := range keys {
			if gotOwners[i] != baseOwners[i] {
				t.Fatalf("排列 %v: key %q 归属 %q != %q",
					perm, keys[i], gotOwners[i], baseOwners[i])
			}
		}
	}
}

// TestRebuildDeterminism 在同一进程内重建两次环，
// 断言每个 key 归属逐个相同（哈希种子固定）。
func TestRebuildDeterminism(t *testing.T) {
	ids := nodeIDs(10)
	keys := genKeys(numKeys)

	r1 := buildRing(t, ids, 150)
	r2 := buildRing(t, ids, 150)

	o1 := locateAll(t, r1, keys)
	o2 := locateAll(t, r2, keys)
	for i := range keys {
		if o1[i] != o2[i] {
			t.Fatalf("key %q 两次建环归属不同: %q vs %q", keys[i], o1[i], o2[i])
		}
	}
}
