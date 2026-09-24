package ontology

import (
	"math/rand"
	"strings"
	"testing"
)

// 同一组节点，20 种不同添加顺序下，环上虚拟节点排布与
// 每个 key 的归属必须逐个完全相同；同进程内重建两次亦必须一致。
func TestRingIsDeterministicAcrossAddOrders(t *testing.T) {
	const nodeCount = 8
	ids := make([]string, nodeCount)
	for i := range ids {
		ids[i] = nodeID(i)
	}
	keys := GenerateKeys(100000)

	build := func(order []string) *Ring {
		r := New()
		for _, id := range order {
			if err := r.Add(id, 100); err != nil {
				t.Fatalf("Add(%s): %v", id, err)
			}
		}
		return r
	}

	reference := build(ids)
	referenceOwners := ownersOf(t, reference, keys)

	rebuilt := build(ids)
	assertSameRing(t, reference, rebuilt, keys, referenceOwners)

	rng := rand.New(rand.NewSource(20260925))
	seen := map[string]bool{strings.Join(ids, ","): true}
	for p := 0; p < 20; p++ {
		perm := append([]string(nil), ids...)
		rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		if seen[strings.Join(perm, ",")] {
			p--
			continue
		}
		seen[strings.Join(perm, ",")] = true
		assertSameRing(t, reference, build(perm), keys, referenceOwners)
	}
}

func assertSameRing(t *testing.T, want, got *Ring, keys []string, wantOwners []string) {
	t.Helper()
	if len(want.points) != len(got.points) {
		t.Fatalf("point count differs: %d vs %d", len(want.points), len(got.points))
	}
	for i := range want.points {
		if want.points[i] != got.points[i] {
			t.Fatalf("point %d differs: %+v vs %+v", i, want.points[i], got.points[i])
		}
	}
	for i, k := range keys {
		owner, err := got.Locate(k)
		if err != nil {
			t.Fatalf("Locate(%q): %v", k, err)
		}
		if owner != wantOwners[i] {
			t.Fatalf("key %q owner differs: %q vs %q", k, owner, wantOwners[i])
		}
	}
}
