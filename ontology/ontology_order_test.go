package ontology

import "testing"

func buildOrderFixture(t *testing.T) (*Store, *Store) {
	t.Helper()

	left := NewStore()
	right := NewStore()
	for _, store := range []*Store{left, right} {
		mustRegister(t, store, "cascade", Cascade, false)
		mustRegister(t, store, "anchor", Cascade, true)
		mustRegister(t, store, "weak", SetNull, false)
		mustRegister(t, store, "protect", SetNull, true)
		mustCreate(t, store, "root", "a", "b", "c", "d", "e", "f")
	}
	links := []Link{
		{Type: "cascade", Source: "root", Target: "a"},
		{Type: "cascade", Source: "a", Target: "b"},
		{Type: "cascade", Source: "b", Target: "root"},
		{Type: "anchor", Source: "a", Target: "c"},
		{Type: "anchor", Source: "b", Target: "d"},
		{Type: "protect", Source: "d", Target: "e"},
		{Type: "weak", Source: "root", Target: "f"},
	}
	for _, link := range links {
		mustLink(t, left, link.Type, link.Source, link.Target)
		mustLink(t, right, link.Type, link.Source, link.Target)
	}
	return left, right
}

func TestDifferentProcessingOrdersProduceSameState(t *testing.T) {
	left, right := buildOrderFixture(t)

	leftResult, err := left.DeleteObjectWithOrder("root", true)
	if err != nil {
		t.Fatal(err)
	}
	rightResult, err := right.DeleteObjectWithOrder("root", false)
	if err != nil {
		t.Fatal(err)
	}

	if !equalStrings(leftResult.DeletedObjects, rightResult.DeletedObjects) {
		t.Fatalf("deleted objects differ: %v vs %v", leftResult.DeletedObjects, rightResult.DeletedObjects)
	}
	if !equalLinks(leftResult.RemovedLinks, rightResult.RemovedLinks) {
		t.Fatalf("removed links differ: %v vs %v", leftResult.RemovedLinks, rightResult.RemovedLinks)
	}
	if !equalLinks(leftResult.NullifiedLinks, rightResult.NullifiedLinks) {
		t.Fatalf("nullified links differ: %v vs %v", leftResult.NullifiedLinks, rightResult.NullifiedLinks)
	}
	if !equalLinks(left.Links(), right.Links()) {
		t.Fatalf("final links differ: %v vs %v", left.Links(), right.Links())
	}

	for _, id := range []string{"a", "b", "c", "d", "e", "f", "root"} {
		if left.HasObject(id) != right.HasObject(id) {
			t.Fatalf("object %q existence differs: left=%v right=%v", id, left.HasObject(id), right.HasObject(id))
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalLinks(left, right []Link) bool {
	if len(left) != len(right) {
		return false
	}
	leftKeys := map[string]int{}
	for _, link := range left {
		leftKeys[linkKey(link)]++
	}
	for _, link := range right {
		key := linkKey(link)
		leftKeys[key]--
		if leftKeys[key] < 0 {
			return false
		}
	}
	return true
}
