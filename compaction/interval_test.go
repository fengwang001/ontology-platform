package compaction

import (
	"fmt"
	"testing"
)

func TestIntervalOverlapQueryIsSublinear(t *testing.T) {
	var previousVisits int
	for exponent := 4; exponent <= 10; exponent++ {
		count := 1 << exponent
		tree := &intervalTree{}
		for index := 0; index < count; index++ {
			left := fmt.Sprintf("%05d", index*2)
			right := fmt.Sprintf("%05d", index*2+1)
			tree.insert(&File{ID: uint64(index + 1), MinKey: []byte(left), MaxKey: []byte(right)})
		}

		height := tree.height()
		_, visits := tree.overlapsWithVisitCount([]byte("00000"), []byte("00001"))
		maxHeight := 3 * ceilLog2PlusOne(count)
		if height > maxHeight {
			t.Fatalf("n=%d height=%d exceeds AVL bound %d", count, height, maxHeight)
		}
		if visits > 2*height {
			t.Fatalf("n=%d visits=%d exceeds O(log n) bound %d", count, visits, 2*height)
		}
		if exponent > 4 {
			factor := float64(count) / float64(count/2)
			growth := float64(visits) / float64(previousVisits)
			if growth >= factor*0.75 {
				t.Fatalf("query visits grew linearly: n factor %.1f visit factor %.2f", factor, growth)
			}
		}
		previousVisits = visits
	}
}

func ceilLog2PlusOne(value int) int {
	bits := 0
	for power := 1; power < value; power <<= 1 {
		bits++
	}
	return bits + 1
}
