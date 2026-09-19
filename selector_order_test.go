package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func TestInvalidCapacity(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		selector, err := NewSelector(k, Desc)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("k=%d: expected ErrInvalidCapacity, got %v", k, err)
		}
		if selector != nil {
			t.Fatalf("k=%d: selector should be nil", k)
		}
	}
}

func TestDescendingScoreOrder(t *testing.T) {
	selector, _ := NewSelector(3, Desc)
	selector.Push("a", 1)
	selector.Push("b", 3)
	selector.Push("c", 2)
	selector.Push("d", 4)

	got := selector.Snapshot()
	want := []Element{{"d", 4}, {"b", 3}, {"c", 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if selector.Len() != 3 {
		t.Fatalf("Len = %d, want 3", selector.Len())
	}
}

func TestAscendingScoreOrder(t *testing.T) {
	selector, _ := NewSelector(3, Asc)
	selector.Push("a", 1)
	selector.Push("b", 3)
	selector.Push("c", 2)
	selector.Push("d", 0)

	got := selector.Snapshot()
	want := []Element{{"d", 0}, {"a", 1}, {"c", 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

func TestSnapshotIncludesAllWhenUnderCapacity(t *testing.T) {
	selector, _ := NewSelector(5, Desc)
	selector.Push("a", 1)
	selector.Push("b", 2)

	got := selector.Snapshot()
	want := []Element{{"b", 2}, {"a", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if selector.Len() != 2 {
		t.Fatalf("Len = %d, want 2", selector.Len())
	}
}
