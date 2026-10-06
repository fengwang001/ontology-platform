package thinpool

import "testing"

func TestBlockMappingSparseRangeOperations(t *testing.T) {
	mapping := newBlockMapping()
	keys := []uint64{0, 1_000_000, 2_000_000, 3_000_000, 4_000_000}
	for index, key := range keys {
		mapping.insert(key, uint64(100-index))
	}

	if count := mapping.countRange(1, 4_000_000); count != 3 {
		t.Fatalf("countRange=%d, want 3", count)
	}
	if count := mapping.countRange(1, 1); count != 0 {
		t.Fatalf("empty range count=%d, want 0", count)
	}

	freed := mapping.deleteRange(500_000, 4_000_001)
	if len(freed) != 4 {
		t.Fatalf("freed %d blocks, want 4", len(freed))
	}
	for index := 0; index < 3; index++ {
		if freed[index] <= freed[index+1] {
			t.Fatalf("freed blocks not in ascending key order: %v", freed)
		}
	}

	snapshot := mapping.snapshot()
	if len(snapshot) != 1 || snapshot[0] != 100 {
		t.Fatalf("unexpected remaining mapping: %+v", snapshot)
	}
}
