package imaging

import (
	"math/rand/v2"
	"sort"
	"testing"
)

type naiveInterval struct {
	key, end int
	id       string
}

func TestIntervalTreeStress(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9))
	tree := newIntervalTree()
	var items []naiveInterval
	active := map[string]bool{}
	for index := 0; index < 100; index++ {
		start := 1000000 + index*100
		id := "h" + itoaBench(index)
		tree.insert("d", interval{key: start, end: start + 90, id: id})
		items = append(items, naiveInterval{start, start + 90, id})
		active[id] = true
	}
	for index := 0; index < 1000; index++ {
		id := "i" + itoaBench(index)
		start := rng.IntN(600)
		end := start + 1 + rng.IntN(30)
		tree.insert("d", interval{key: start, end: end, id: id})
		items = append(items, naiveInterval{start, end, id})
		active[id] = true
		if index%5 == 0 && len(items) > 0 {
			target := items[rng.IntN(len(items))]
			if active[target.id] {
				node := findIntervalTest(tree.roots["d"], target.id)
				tree.remove("d", target.key, target.id, node.seq)
				active[target.id] = false
				tree.restore("d", interval{key: target.key, end: target.end, id: target.id, seq: node.seq, priority: node.priority})
				active[target.id] = true
			}
		}
		for range 20 {
			queryStart := rng.IntN(600)
			queryEnd := queryStart + 1 + rng.IntN(40)
			got := tree.overlaps("d", queryStart, queryEnd, "")
			var want []string
			for _, item := range items {
				if active[item.id] && queryStart < item.end && item.key < queryEnd {
					want = append(want, item.id)
				}
			}
			sort.Strings(want)
			if len(got) != len(want) {
				t.Fatalf("index=%d query=[%d,%d) got=%v want=%v", index, queryStart, queryEnd, got, want)
			}
			for j := range got {
				if got[j] != want[j] {
					t.Fatalf("index=%d mismatch got=%v want=%v", index, got, want)
				}
			}
		}
	}
}

func TestIntervalTreeDuplicateStartInsertions(t *testing.T) {
	tree := newIntervalTree()
	for index := 0; index < 500; index++ {
		id := "d" + itoaBench(index)
		tree.insert("d", interval{key: 100, end: 130, id: id})
		got := tree.overlaps("d", 100, 130, "")
		if len(got) != index+1 {
			t.Fatalf("after %d insertions overlaps=%d, want %d", index+1, len(got), index+1)
		}
	}
}

func TestIntervalTreeBenchmarkShape(t *testing.T) {
	tree := newIntervalTree()
	for index := 0; index < 16000; index++ {
		start := 1000000 + index*100
		tree.insert("d", interval{key: start, end: start + 30, id: "h" + itoaBench(index)})
	}
	for index := 0; index < 1000; index++ {
		start := 100 + index*100
		id := "c" + itoaBench(index)
		tree.insert("d", interval{key: start, end: start + 30, id: id})
		if got := tree.overlaps("d", start, start+30, ""); len(got) != 1 {
			t.Fatalf("index=%d overlaps=%v", index, got)
		}
	}
}

func findIntervalTest(node *intervalNode, id string) *intervalNode {
	if node == nil {
		return nil
	}
	if node.id == id {
		return node
	}
	if found := findIntervalTest(node.left, id); found != nil {
		return found
	}
	return findIntervalTest(node.right, id)
}

func itoaBench(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
