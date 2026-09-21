package quorum

import "testing"

// Regression: majority must be strictly greater than n/2, so for even
// n a bare half is NOT enough; the old (n+1)/2 formula let 2 of 4
// replicas commit.
func TestMajorityEvenClusterNeedsStrictHalf(t *testing.T) {
	if got := Majority(4); got != 3 {
		t.Fatalf("Majority(4) = %d, want 3", got)
	}
	// Two of four acks must not raise the commit point.
	if got := CommitIndex([]uint64{9, 9, 0, 0}, 4); got != 0 {
		t.Fatalf("CommitIndex with 2/4 acks = %d, want 0", got)
	}
	// Three of four acks commit at the third-highest match.
	if got := CommitIndex([]uint64{9, 9, 5, 0}, 4); got != 5 {
		t.Fatalf("CommitIndex with 3/4 acks = %d, want 5", got)
	}
}
