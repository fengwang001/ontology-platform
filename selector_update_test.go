package ontology

import (
	"math"
	"reflect"
	"testing"
)

func TestNaNRejectedAndCounted(t *testing.T) {
	selector, _ := NewSelector(2, Desc)
	if selector.Push("a", math.NaN()) {
		t.Fatal("NaN Push should report false")
	}
	selector.Push("b", math.Float64frombits(math.Float64bits(math.NaN())^1))
	selector.Push("c", 1)

	if got := selector.Skipped(); got != 2 {
		t.Fatalf("Skipped = %d, want 2", got)
	}
	got := selector.Snapshot()
	want := []Element{{"c", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if selector.Len() != 1 {
		t.Fatalf("Len = %d, want 1", selector.Len())
	}
}

func TestInfinityRanksNormally(t *testing.T) {
	selector, _ := NewSelector(3, Desc)
	selector.Push("neg", math.Inf(-1))
	selector.Push("finite", 0)
	selector.Push("pos", math.Inf(1))

	got := selector.Snapshot()
	want := []Element{{"pos", math.Inf(1)}, {"finite", 0}, {"neg", math.Inf(-1)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("infinity order = %v, want %v", got, want)
	}
}

func TestDuplicateIDOverwritesAndCanFallOutOfTopK(t *testing.T) {
	selector, _ := NewSelector(2, Desc)
	selector.Push("a", 10)
	selector.Push("b", 8)
	selector.Push("a", 1)

	got := selector.Snapshot()
	want := []Element{{"b", 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after overwrite = %v, want %v", got, want)
	}

	ids := map[string]bool{}
	for _, element := range got {
		if ids[element.ID] {
			t.Fatalf("duplicate ID in snapshot: %q", element.ID)
		}
		ids[element.ID] = true
	}
}

func TestDuplicateIDOverwriteCanImproveRank(t *testing.T) {
	selector, _ := NewSelector(2, Desc)
	selector.Push("a", 1)
	selector.Push("b", 5)
	selector.Push("c", 4)
	selector.Push("a", 6)

	got := selector.Snapshot()
	want := []Element{{"a", 6}, {"b", 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after improved overwrite = %v, want %v", got, want)
	}
	if selector.Len() != 2 {
		t.Fatalf("Len = %d, want 2", selector.Len())
	}
}

func TestLongStreamNeverExceedsCapacity(t *testing.T) {
	const k = 7
	selector, _ := NewSelector(k, Desc)
	for i := 0; i < 1000; i++ {
		selector.Push(string(rune('a'+i%26))+string(rune('a'+i/26%26)), float64(i))
		if selector.Len() > k {
			t.Fatalf("after %d pushes Len = %d, exceeds %d", i+1, selector.Len(), k)
		}
	}
	if selector.Len() != k {
		t.Fatalf("final Len = %d, want %d", selector.Len(), k)
	}
}
