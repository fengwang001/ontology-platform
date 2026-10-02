package boundedhash

import (
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, r *Ring, id uint64, points ...uint64) {
	t.Helper()
	if err := r.AddNode(id, points); err != nil {
		t.Fatalf("AddNode(%d, %v): %v", id, points, err)
	}
}

func mustPut(t *testing.T, r *Ring, key string, pos uint64) {
	t.Helper()
	if err := r.Put(key, pos); err != nil {
		t.Fatalf("Put(%q, %d): %v", key, pos, err)
	}
}

func lookup(t *testing.T, r *Ring, key string) uint64 {
	t.Helper()
	id, err := r.Lookup(key)
	if err != nil {
		t.Fatalf("Lookup(%q): %v", key, err)
	}
	return id
}

func loads(r *Ring) map[uint64]int {
	result := make(map[uint64]int, len(r.nodes))
	for id, node := range r.nodes {
		result[id] = len(node.keys)
	}
	return result
}

func TestSpecifiedExampleAndRemoveNode(t *testing.T) {
	r, err := New(5, 4)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10, 50)
	mustAdd(t, r, 2, 30, 70)
	mustAdd(t, r, 3, 90)

	positions := map[string]uint64{
		"k1": 5,
		"k2": 8,
		"k3": 12,
		"k4": 14,
		"k5": 95,
		"k6": 92,
		"k7": 91,
	}
	for _, key := range []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7"} {
		mustPut(t, r, key, positions[key])
	}

	wantOwners := map[string]uint64{
		"k1": 1,
		"k2": 2,
		"k3": 2,
		"k4": 1,
		"k5": 1,
		"k6": 2,
		"k7": 3,
	}
	for key, want := range wantOwners {
		if got := lookup(t, r, key); got != want {
			t.Fatalf("Lookup(%q)=%d, want %d", key, got, want)
		}
	}
	if got := loads(r); !reflect.DeepEqual(got, map[uint64]int{1: 3, 2: 3, 3: 1}) {
		t.Fatalf("loads=%v", got)
	}

	if err := r.RemoveNode(2); err != nil {
		t.Fatal(err)
	}
	wantAfterRemove := map[string]uint64{"k2": 1, "k3": 3, "k6": 1}
	for key, want := range wantAfterRemove {
		if got := lookup(t, r, key); got != want {
			t.Fatalf("after RemoveNode Lookup(%q)=%d, want %d", key, got, want)
		}
	}
	if got := loads(r); !reflect.DeepEqual(got, map[uint64]int{1: 5, 3: 2}) {
		t.Fatalf("loads after RemoveNode=%v", got)
	}
}

func TestRebalance(t *testing.T) {
	newPostRemove := func(t *testing.T) *Ring {
		r, err := New(5, 4)
		if err != nil {
			t.Fatal(err)
		}
		mustAdd(t, r, 1, 10, 50)
		mustAdd(t, r, 2, 30, 70)
		mustAdd(t, r, 3, 90)
		for _, item := range []struct {
			key string
			pos uint64
		}{
			{"k1", 5}, {"k2", 8}, {"k3", 12}, {"k4", 14}, {"k5", 95}, {"k6", 92}, {"k7", 91},
		} {
			mustPut(t, r, item.key, item.pos)
		}
		if err := r.RemoveNode(2); err != nil {
			t.Fatal(err)
		}
		mustAdd(t, r, 4, 60)
		return r
	}

	t.Run("limit ten drains all excess", func(t *testing.T) {
		r := newPostRemove(t)
		migrations, excess, err := r.Rebalance(10)
		if err != nil {
			t.Fatal(err)
		}
		wantMigrations := []Migration{
			{Key: "k6", FromNode: 1, ToNode: 4},
			{Key: "k5", FromNode: 1, ToNode: 4},
		}
		if !reflect.DeepEqual(migrations, wantMigrations) {
			t.Fatalf("migrations=%+v, want %+v", migrations, wantMigrations)
		}
		if excess != 0 {
			t.Fatalf("excess=%d, want 0", excess)
		}
		if got := loads(r); !reflect.DeepEqual(got, map[uint64]int{1: 3, 3: 2, 4: 2}) {
			t.Fatalf("loads=%v", got)
		}
	})

	t.Run("limit one stops with one excess", func(t *testing.T) {
		r := newPostRemove(t)
		migrations, excess, err := r.Rebalance(1)
		if err != nil {
			t.Fatal(err)
		}
		want := []Migration{{Key: "k6", FromNode: 1, ToNode: 4}}
		if !reflect.DeepEqual(migrations, want) {
			t.Fatalf("migrations=%+v, want %+v", migrations, want)
		}
		if excess != 1 {
			t.Fatalf("excess=%d, want 1", excess)
		}
	})
}

func TestSamePositionsAndWrap(t *testing.T) {
	r, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10)
	mustAdd(t, r, 2, 20)
	mustPut(t, r, "a", 25)
	mustPut(t, r, "b", 25)
	mustPut(t, r, "c", 25)
	mustPut(t, r, "d", 25)

	want := map[string]uint64{"a": 1, "b": 2, "c": 1, "d": 2}
	for key, node := range want {
		if got := lookup(t, r, key); got != node {
			t.Fatalf("Lookup(%q)=%d, want %d", key, got, node)
		}
	}
}

func TestLoadEqualsCapacityIsFull(t *testing.T) {
	r, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10)
	mustAdd(t, r, 2, 20)
	mustPut(t, r, "a", 5)
	mustPut(t, r, "b", 5)
	mustPut(t, r, "c", 5)

	want := map[string]uint64{"a": 1, "b": 2, "c": 1}
	for key, node := range want {
		if got := lookup(t, r, key); got != node {
			t.Fatalf("Lookup(%q)=%d, want %d", key, got, node)
		}
	}
}

func TestAddNodeDoesNotMoveKeysAndSkipsAllFullPoints(t *testing.T) {
	r, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10, 50)
	mustPut(t, r, "a", 5)
	mustPut(t, r, "b", 5)
	mustAdd(t, r, 2, 20)

	ownerA := lookup(t, r, "a")
	ownerB := lookup(t, r, "b")
	if ownerA != 1 || ownerB != 1 {
		t.Fatalf("AddNode moved existing keys: a=%d b=%d", lookup(t, r, "a"), lookup(t, r, "b"))
	}
	mustPut(t, r, "c", 25)
	if got := lookup(t, r, "c"); got != 2 {
		t.Fatalf("c owner=%d, want 2 after skipping both full node points", got)
	}
}

func TestLastNodeRemoval(t *testing.T) {
	r, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10)
	if err := r.RemoveNode(1); err != nil {
		t.Fatalf("remove empty last node: %v", err)
	}
	mustAdd(t, r, 1, 10)
	mustPut(t, r, "a", 5)
	if err := r.RemoveNode(1); !errors.Is(err, ErrCannotClearLastNode) {
		t.Fatalf("remove non-empty last node error=%v", err)
	}
	if got := lookup(t, r, "a"); got != 1 {
		t.Fatalf("rejected removal changed key owner to %d", got)
	}
}

func TestDeleteDoesNotMigrate(t *testing.T) {
	r, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10)
	mustAdd(t, r, 2, 20)
	mustPut(t, r, "a", 5)
	mustPut(t, r, "b", 5)
	mustPut(t, r, "c", 5)

	if err := r.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, r, "b"); got != 2 {
		t.Fatalf("b moved to %d", got)
	}
	if got := lookup(t, r, "c"); got != 1 {
		t.Fatalf("c moved to %d", got)
	}
	if _, err := r.Lookup("a"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("deleted key lookup error=%v", err)
	}
	if got := loads(r); !reflect.DeepEqual(got, map[uint64]int{1: 1, 2: 1}) {
		t.Fatalf("loads=%v", got)
	}
}

func TestRejectedOperationsDoNotMutate(t *testing.T) {
	r, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10)
	mustPut(t, r, "a", 5)

	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"put empty", func() error { return r.Put("", 1) }, ErrInvalidArgument},
		{"put duplicate", func() error { return r.Put("a", 1) }, ErrKeyExists},
		{"add bad id", func() error { return r.AddNode(0, []uint64{20}) }, ErrInvalidArgument},
		{"add no points", func() error { return r.AddNode(2, nil) }, ErrInvalidArgument},
		{"add duplicate point", func() error { return r.AddNode(2, []uint64{20, 20}) }, ErrInvalidArgument},
		{"add existing node", func() error { return r.AddNode(1, []uint64{20}) }, ErrNodeExists},
		{"add conflicting point", func() error { return r.AddNode(2, []uint64{10}) }, ErrPointConflict},
		{"remove missing node", func() error { return r.RemoveNode(9) }, ErrNodeNotFound},
		{"delete empty", func() error { return r.Delete("") }, ErrInvalidArgument},
		{"delete missing", func() error { return r.Delete("missing") }, ErrKeyNotFound},
	}
	for _, tt := range tests {
		if err := tt.call(); !errors.Is(err, tt.want) {
			t.Fatalf("%s: error=%v, want %v", tt.name, err, tt.want)
		}
	}

	if len(r.nodes) != 1 || len(r.points) != 1 || r.totalKeys != 1 || r.nextSeq != 2 {
		t.Fatalf("state mutated by rejected calls: nodes=%d points=%d keys=%d seq=%d",
			len(r.nodes), len(r.points), r.totalKeys, r.nextSeq)
	}
	if got := lookup(t, r, "a"); got != 1 {
		t.Fatalf("a owner=%d after rejected calls", got)
	}
}

func TestValidationOrderAndConfig(t *testing.T) {
	r, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lookup(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Lookup empty: %v", err)
	}
	if _, _, err := r.Rebalance(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Rebalance negative: %v", err)
	}
	if _, _, err := r.Rebalance(0); !errors.Is(err, ErrNoNode) {
		t.Fatalf("Rebalance empty ring: %v", err)
	}

	for _, c := range []struct{ n, d int }{{0, 1}, {1, 2}, {2, 3}, {1_000_001, 1}, {2, 0}} {
		if _, err := New(c.n, c.d); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%d,%d)=%v, want invalid", c.n, c.d, err)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	r, err := New(3, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10, 40)
	mustAdd(t, r, 2, 20, 50)
	mustAdd(t, r, 3, 30, 60)

	var readers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = r.Put(fmt.Sprintf("c-%d", i), uint64(i%70))
		}
	}()

	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func(offset int) {
			defer readers.Done()
			<-done
			for j := 0; j < 100; j++ {
				_, _ = r.Lookup(fmt.Sprintf("c-%d", (j+offset)%100))
				_, _, _ = r.Rebalance(int(j % 20))
				_ = r.Loads()
			}
		}(i)
	}

	<-done
	readers.Wait()
	if len(r.keys) != 100 || r.totalKeys != 100 {
		t.Fatalf("keys=%d total=%d", len(r.keys), r.totalKeys)
	}
	sum := 0
	for _, load := range r.Loads() {
		sum += load
	}
	if sum != 100 {
		t.Fatalf("load sum=%d", sum)
	}
}

func TestBinarySearchComparisonBound(t *testing.T) {
	for _, pointCount := range []int{100, 100_000} {
		r, err := New(1, 1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < pointCount; i++ {
			r.points = append(r.points, ringPoint{pos: uint64(i * 2), nodeID: uint64(i + 1)})
		}

		allowed := bits.Len(uint(pointCount-1)) + 1
		for i := 0; i <= pointCount*2; i++ {
			pos := uint64(i)
			before := r.binarySearchComparisonCountLocked()
			r.lowerBoundLocked(pos)
			used := r.binarySearchComparisonCountLocked() - before
			if used > int64(allowed) {
				t.Fatalf("P=%d pos=%d used %d comparisons, bound %d", pointCount, pos, used, allowed)
			}
		}
		t.Logf("binary search P=%d bound=%d", pointCount, allowed)
	}
}

func TestInvariantSummary(t *testing.T) {
	r, err := New(5, 4)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, r, 1, 10, 50)
	mustAdd(t, r, 2, 30, 70)
	mustPut(t, r, "a", 5)
	mustPut(t, r, "b", 8)
	sum := 0
	for _, node := range r.nodes {
		sum += len(node.keys)
	}
	if sum != r.totalKeys {
		t.Fatalf("load sum=%d totalKeys=%d", sum, r.totalKeys)
	}
	fmt.Printf("summary: keys=%d loads=%v pointCount=%d nextSeq=%d\n", r.totalKeys, loads(r), len(r.points), r.nextSeq)
}
