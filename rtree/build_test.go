package rtree

import "testing"

// buildHeight2Tree assembles root(A(A1,A2),B(B1..Bk)) where each leaf
// holds two objects whose point-rectangles are (id*1000, id*1000), so all
// MBRs are disjoint and subtree reinsertion targets are deterministic.
func buildHeight2Tree(t *testing.T, bLeaves, M int) *RTree {
	t.Helper()
	tree, err := New(M, 2, 1000)
	if err != nil {
		t.Fatal(err)
	}
	makeLeaf := func(id1, id2 int64) *node {
		r := func(id int64) Rect {
			return Rect{id * 1000, id * 1000, id*1000 + 1, id*1000 + 1}
		}
		tree.objects[id1] = r(id1)
		tree.objects[id2] = r(id2)
		return &node{height: 0, entries: []entry{
			{id: id1, rect: r(id1)},
			{id: id2, rect: r(id2)},
		}}
	}
	a1 := makeLeaf(1, 2)
	a2 := makeLeaf(3, 4)
	a := &node{height: 1, entries: []entry{
		{rect: a1.mbr(), child: a1},
		{rect: a2.mbr(), child: a2},
	}}
	var bChildren []entry
	for i := 0; i < bLeaves; i++ {
		id1 := int64(5 + 2*i)
		leaf := makeLeaf(id1, id1+1)
		bChildren = append(bChildren, entry{rect: leaf.mbr(), child: leaf})
	}
	b := &node{height: 1, entries: bChildren}
	tree.root = &node{height: 2, entries: []entry{
		{rect: a.mbr(), child: a},
		{rect: b.mbr(), child: b},
	}}
	tree.count = 4 + 2*bLeaves
	if err := checkInvariants(tree); err != nil {
		t.Fatalf("buildHeight2Tree invariants: %v", err)
	}
	return tree
}
