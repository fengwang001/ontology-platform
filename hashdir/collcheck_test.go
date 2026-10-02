package hashdir

import "testing"

func TestCollisionGroupsFound(t *testing.T) {
	t.Logf("coll=%v pair=%v", collNames, pairNames)
	if len(collNames) != 3 || len(pairNames) != 2 {
		t.Fatal("groups not found")
	}
	hc := hashName(collSeed, collNames[0])
	for _, n := range collNames[1:] {
		if hashName(collSeed, n) != hc {
			t.Fatal("coll group mismatch")
		}
	}
	hp := hashName(collSeed, pairNames[0])
	if hashName(collSeed, pairNames[1]) != hp || hp == hc {
		t.Fatal("pair group mismatch")
	}
}
