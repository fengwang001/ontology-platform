package ontology

import (
	"math"
	"reflect"
	"testing"
)

func TestTiesUseIDOrderIndependentOfDirection(t *testing.T) {
	elements := []Element{{"c", 1}, {"a", 1}, {"b", 1}}

	desc, _ := NewSelector(3, Desc)
	asc, _ := NewSelector(3, Asc)
	for _, element := range elements {
		desc.Push(element.ID, element.Score)
		asc.Push(element.ID, element.Score)
	}

	want := []Element{{"a", 1}, {"b", 1}, {"c", 1}}
	if got := desc.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Desc ties = %v, want %v", got, want)
	}
	if got := asc.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Asc ties = %v, want %v", got, want)
	}
}

func TestTieAtBoundaryKeepsLexicographicallySmallerID(t *testing.T) {
	for _, direction := range []Direction{Desc, Asc} {
		selector, _ := NewSelector(2, direction)
		selector.Push("alpha", 5)
		selector.Push("charlie", 5)
		selector.Push("bravo", 5)

		got := selector.Snapshot()
		want := []Element{{"alpha", 5}, {"bravo", 5}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("direction %d: got %v, want %v", direction, got, want)
		}
	}
}

func TestSnapshotIndependentOfArrivalOrder(t *testing.T) {
	base := []Element{
		{"d", 4}, {"a", 1}, {"f", 1}, {"c", 4},
		{"b", 2}, {"e", 2}, {"g", 3},
	}
	first := snapshotFrom(5, Desc, base)
	second := snapshotFrom(5, Desc, []Element{
		base[6], base[2], base[5], base[0], base[3], base[4], base[1],
	})

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("shuffled snapshots differ: %v vs %v", first, second)
	}
}

func TestSignedZeroTiesUseID(t *testing.T) {
	selector, _ := NewSelector(2, Desc)
	selector.Push("b", math.Copysign(0, -1))
	selector.Push("a", 0)

	got := selector.Snapshot()
	want := []Element{{"a", 0}, {"b", math.Copysign(0, -1)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signed zero order = %v, want %v", got, want)
	}
}

func snapshotFrom(k int, direction Direction, elements []Element) []Element {
	selector, _ := NewSelector(k, direction)
	for _, element := range elements {
		selector.Push(element.ID, element.Score)
	}
	return selector.Snapshot()
}
