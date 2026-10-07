package imaging

import (
	"math/rand/v2"
	"testing"
)

func TestIntervalTreeOverlapsAndRemoval(t *testing.T) {
	tree := newIntervalTree()
	tree.insert("d", interval{key: 10, end: 20, id: "a"})
	tree.insert("d", interval{key: 20, end: 30, id: "b"})
	tree.insert("d", interval{key: 15, end: 16, id: "c"})
	got := tree.overlaps("d", 20, 30, "")
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("touching interval overlap = %v, want [b]", got)
	}
	b := tree.roots["d"]
	for b.id != "b" {
		if b.key > 20 {
			b = b.left
		} else {
			b = b.right
		}
	}
	tree.remove("d", 20, "b", b.seq)
	if overlaps := tree.overlaps("d", 10, 100, ""); len(overlaps) != 2 {
		t.Fatalf("after removal overlaps = %v, want 2", overlaps)
	}
}

func TestCountTreeMatchesNaiveDifferenceArray(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	tree := &countTree{}
	events := map[int]int{}
	for range 300 {
		start := rng.IntN(300)
		end := start + 1 + rng.IntN(40)
		delta := 1
		if rng.IntN(3) == 0 {
			delta = -1
		}
		events[start] += delta
		events[end] -= delta
		tree.addRange(start, end, delta)
		if err := verifyCountTree(tree.root); err != nil {
			t.Fatal(err)
		}
		queryStart := rng.IntN(300)
		queryEnd := queryStart + 1 + rng.IntN(50)
		current, naiveBest := 0, 0
		for point := 0; point < queryEnd; point++ {
			current += events[point]
			if point >= queryStart && current > naiveBest {
				naiveBest = current
			}
		}
		baseline := 0
		for point, delta := range events {
			if point < queryStart {
				baseline += delta
			}
		}
		naiveBest = max(naiveBest, baseline)
		naiveBest = max(0, naiveBest)
		if got := tree.maxOnRange(queryStart, queryEnd); got != naiveBest {
			t.Fatalf("maxOnRange(%d,%d)=%d, want %d", queryStart, queryEnd, got, naiveBest)
		}
	}
}

func verifyCountTree(node *countNode) error {
	if node == nil {
		return nil
	}
	if err := verifyCountTree(node.left); err != nil {
		return err
	}
	if err := verifyCountTree(node.right); err != nil {
		return err
	}
	gotSum := countSum(node.left) + node.value + countSum(node.right)
	if gotSum != node.sum {
		return tError("count sum mismatch")
	}
	return nil
}

type testError string

func (e testError) Error() string { return string(e) }

func tError(message string) error { return testError(message) }
