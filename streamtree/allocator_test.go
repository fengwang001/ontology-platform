package streamtree

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func TestExclusiveInsertAndAllocation(t *testing.T) {
	var logs bytes.Buffer
	allocator := New(&logs)

	mustOpen(t, allocator, 1, 0, 10, false)
	mustOpen(t, allocator, 2, 1, 2, false)
	mustOpen(t, allocator, 3, 1, 3, false)
	mustOpen(t, allocator, 5, 1, 1, false)
	mustOpen(t, allocator, 4, 1, 5, true)

	assertParent(t, allocator, 2, 4)
	assertParent(t, allocator, 3, 4)
	assertParent(t, allocator, 5, 4)
	assertParent(t, allocator, 4, 1)

	shares, err := allocator.Allocate(10, 2, 3, 5)
	if err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	assertShares(t, shares, map[int]int{2: 3, 3: 5, 5: 2})

	logText := logs.String()
	assertContains(t, logText, "exclusive=true")
	assertContains(t, logText, "input={quota:10 ready:[2,3,5]}")
	assertContains(t, logText, "output=map[2:3 3:5 5:2]")
	assertContains(t, logText, "ties use smaller stream id")
	t.Logf("exclusive insertion and allocation log:\n%s", logText)
}

func TestResetToOwnDescendant(t *testing.T) {
	allocator := New(nil)

	mustOpen(t, allocator, 1, 0, 10, false)
	mustOpen(t, allocator, 2, 1, 20, false)
	mustOpen(t, allocator, 3, 2, 30, false)

	if err := allocator.Reset(1, 3, 40, false); err != nil {
		t.Fatalf("Reset returned error: %v", err)
	}

	assertParent(t, allocator, 3, 0)
	assertParent(t, allocator, 1, 3)
	assertParent(t, allocator, 2, 1)
	if weight := allocator.streams[3].weight; weight != 30 {
		t.Fatalf("moved descendant weight = %d, want 30", weight)
	}
	if weight := allocator.streams[1].weight; weight != 40 {
		t.Fatalf("reset stream weight = %d, want 40", weight)
	}
	assertAcyclicAndSingleParent(t, allocator)
}

func TestCloseRedistributesWeightsWithFloorOne(t *testing.T) {
	allocator := New(nil)

	mustOpen(t, allocator, 1, 0, 10, false)
	mustOpen(t, allocator, 2, 1, 2, false)
	mustOpen(t, allocator, 3, 1, 3, false)
	mustOpen(t, allocator, 4, 1, 4, false)

	if err := allocator.Close(1); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if _, exists := allocator.streams[1]; exists {
		t.Fatal("closed stream still exists")
	}
	assertParent(t, allocator, 2, 0)
	assertParent(t, allocator, 3, 0)
	assertParent(t, allocator, 4, 0)
	assertWeight(t, allocator, 2, 2)
	assertWeight(t, allocator, 3, 3)
	assertWeight(t, allocator, 4, 4)

	mustOpen(t, allocator, 5, 0, 1, false)
	mustOpen(t, allocator, 6, 5, 10, false)
	mustOpen(t, allocator, 7, 5, 20, false)
	if err := allocator.Close(5); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	assertWeight(t, allocator, 6, 1)
	assertWeight(t, allocator, 7, 1)
	assertAcyclicAndSingleParent(t, allocator)
}

func TestRemainderTieUsesSmallerID(t *testing.T) {
	allocator := New(nil)

	mustOpen(t, allocator, 2, 0, 1, false)
	mustOpen(t, allocator, 3, 0, 1, false)

	shares, err := allocator.Allocate(1, 2, 3)
	if err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	assertShares(t, shares, map[int]int{2: 1, 3: 0})
}

func TestReadyParentBlocksDescendants(t *testing.T) {
	allocator := New(nil)

	mustOpen(t, allocator, 1, 0, 1, false)
	mustOpen(t, allocator, 2, 1, 1, false)
	mustOpen(t, allocator, 3, 0, 1, false)

	shares, err := allocator.Allocate(5, 1, 2, 3)
	if err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	assertShares(t, shares, map[int]int{1: 3, 3: 2})
}

func TestNoReadyStreamsReturnsZeros(t *testing.T) {
	allocator := New(nil)

	mustOpen(t, allocator, 1, 0, 10, false)
	shares, err := allocator.Allocate(8)
	if err != nil {
		t.Fatalf("Allocate returned error: %v", err)
	}
	assertShares(t, shares, map[int]int{})
}

func TestRejectionsDoNotMutateTree(t *testing.T) {
	allocator := New(nil)
	mustOpen(t, allocator, 1, 0, 10, false)

	if err := allocator.Open(1, 0, 10, false); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("duplicate Open error = %v, want %v", err, ErrInvalidID)
	}
	if err := allocator.Open(2, 99, 10, false); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("missing parent error = %v, want %v", err, ErrParentNotFound)
	}
	if err := allocator.Open(2, 1, 257, false); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("invalid weight error = %v, want %v", err, ErrInvalidWeight)
	}
	if err := allocator.Reset(9, 1, 10, false); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("missing reset stream error = %v, want %v", err, ErrStreamNotFound)
	}
	if err := allocator.Reset(1, 9, 10, false); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("missing reset parent error = %v, want %v", err, ErrParentNotFound)
	}
	if err := allocator.Reset(1, 1, 10, false); !errors.Is(err, ErrSelfParent) {
		t.Fatalf("self parent error = %v, want %v", err, ErrSelfParent)
	}
	if err := allocator.Reset(1, 0, 0, false); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("reset invalid weight error = %v, want %v", err, ErrInvalidWeight)
	}
	if err := allocator.Close(9); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("missing close error = %v, want %v", err, ErrStreamNotFound)
	}
	if _, err := allocator.Allocate(-1); !errors.Is(err, ErrNegativeQuota) {
		t.Fatalf("negative quota error = %v, want %v", err, ErrNegativeQuota)
	}
	if _, err := allocator.Allocate(5, 9); !errors.Is(err, ErrReadyStreamNotFound) {
		t.Fatalf("missing ready stream error = %v, want %v", err, ErrReadyStreamNotFound)
	}

	assertParent(t, allocator, 1, 0)
	assertWeight(t, allocator, 1, 10)
	if len(allocator.streams) != 1 {
		t.Fatalf("stream count = %d, want 1", len(allocator.streams))
	}
}

func TestConcurrentOperations(t *testing.T) {
	allocator := New(nil)
	for id := 1; id <= 8; id++ {
		mustOpen(t, allocator, id, 0, id%256+1, false)
	}

	var wait sync.WaitGroup
	for id := 9; id <= 40; id++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			parent := (id-1)%8 + 1
			if err := allocator.Open(id, parent, (id%256)+1, id%4 == 0); err != nil {
				t.Errorf("Open(%d): %v", id, err)
			}
		}(id)
	}
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			ready := []int{1 + index%8, 1 + (index+1)%8}
			shares, err := allocator.Allocate(100+index, ready...)
			if err != nil {
				t.Errorf("Allocate: %v", err)
				return
			}
			total := 0
			for _, share := range shares {
				total += share
			}
			if total != 100+index {
				t.Errorf("allocated total = %d, want %d", total, 100+index)
			}
		}(index)
	}
	wait.Wait()
	assertAcyclicAndSingleParent(t, allocator)
}

func mustOpen(t *testing.T, allocator *Allocator, id, parent, weight int, exclusive bool) {
	t.Helper()
	if err := allocator.Open(id, parent, weight, exclusive); err != nil {
		t.Fatalf("Open(%d, parent=%d, weight=%d, exclusive=%t): %v", id, parent, weight, exclusive, err)
	}
}

func assertParent(t *testing.T, allocator *Allocator, id, want int) {
	t.Helper()
	if got := allocator.streams[id].parent; got != want {
		t.Fatalf("stream %d parent = %d, want %d", id, got, want)
	}
}

func assertWeight(t *testing.T, allocator *Allocator, id, want int) {
	t.Helper()
	if got := allocator.streams[id].weight; got != want {
		t.Fatalf("stream %d weight = %d, want %d", id, got, want)
	}
}

func assertShares(t *testing.T, got, want map[int]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("shares = %v, want %v", got, want)
	}
	for id, share := range want {
		if got[id] != share {
			t.Fatalf("share[%d] = %d, want %d; all shares=%v", id, got[id], share, got)
		}
	}
	for id, share := range got {
		if _, ok := want[id]; !ok || share != want[id] {
			t.Fatalf("unexpected share[%d]=%d; all shares=%v, want %v", id, share, got, want)
		}
	}
}

func assertContains(t *testing.T, text, want string) {
	t.Helper()
	if !bytes.Contains([]byte(text), []byte(want)) {
		t.Fatalf("logs do not contain %q\nlogs:\n%s", want, text)
	}
}

func assertAcyclicAndSingleParent(t *testing.T, allocator *Allocator) {
	t.Helper()
	for id, node := range allocator.streams {
		if node.parent != RootID && allocator.streams[node.parent] == nil {
			t.Fatalf("stream %d points to missing parent %d", id, node.parent)
		}
		if _, ok := allocator.children[node.parent][id]; !ok {
			t.Fatalf("stream %d is missing from parent %d children", id, node.parent)
		}

		seen := map[int]struct{}{}
		for current := id; current != RootID; current = allocator.streams[current].parent {
			if _, repeated := seen[current]; repeated {
				t.Fatalf("cycle detected starting at stream %d", id)
			}
			seen[current] = struct{}{}
		}
	}

	for parent, children := range allocator.children {
		if parent != RootID && allocator.streams[parent] == nil {
			t.Fatalf("children exist for dead parent %d", parent)
		}
		for child := range children {
			if allocator.streams[child] == nil {
				t.Fatalf("dead stream %d remains under parent %d", child, parent)
			}
			if allocator.streams[child].parent != parent {
				t.Fatalf("child %d parent = %d, child set parent = %d", child, allocator.streams[child].parent, parent)
			}
		}
	}
}
