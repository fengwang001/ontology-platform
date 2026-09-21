package quorum

import "testing"

// Regression: Majority used (n+1)/2, which undercounts even-sized
// clusters (e.g. n=4 yielded 2 instead of 3), letting a bare half
// pass as a majority.
func TestMajorityEvenClusterRegression(t *testing.T) {
	if got := Majority(4); got != 3 {
		t.Fatalf("Majority(4) = %d, want 3: two acks must not suffice", got)
	}
	// With the threshold fixed, 2-of-4 matches must not commit.
	if got := CommitIndex([]uint64{5, 5, 0, 0}, 4); got != 0 {
		t.Fatalf("CommitIndex with 2-of-4 acks = %d, want 0", got)
	}
	if got := CommitIndex([]uint64{5, 5, 5, 0}, 4); got != 5 {
		t.Fatalf("CommitIndex with 3-of-4 acks = %d, want 5", got)
	}
}
