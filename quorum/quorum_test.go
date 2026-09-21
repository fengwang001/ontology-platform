package quorum

import "testing"

// Semantics 2: majority thresholds for n = 1..7.
func TestMajority(t *testing.T) {
	want := []int{0, 1, 2, 2, 3, 3, 4, 4} // index is n
	for n := 1; n <= 7; n++ {
		if got := Majority(n); got != want[n] {
			t.Errorf("Majority(%d) = %d, want %d", n, got, want[n])
		}
	}
	if got := Majority(0); got != 0 {
		t.Errorf("Majority(0) = %d, want 0", got)
	}
}

// Semantics 2: CommitIndex only advances to what a majority matched;
// a minority running ahead must never raise it.
func TestCommitIndex(t *testing.T) {
	cases := []struct {
		name    string
		matches []uint64
		n       int
		want    uint64
	}{
		{"all equal", []uint64{5, 5, 5}, 3, 5},
		{"minority ahead", []uint64{9, 2, 2}, 3, 2},
		{"exact majority", []uint64{7, 7, 1}, 3, 7},
		{"below majority", []uint64{7, 1, 1}, 3, 1},
		{"five nodes", []uint64{4, 4, 4, 1, 1}, 5, 4},
		{"five nodes two ahead", []uint64{8, 8, 3, 3, 3}, 5, 3},
		{"single node", []uint64{6}, 1, 6},
		{"zero matches", []uint64{0, 0, 0}, 3, 0},
		{"empty", nil, 3, 0},
		{"unsorted input", []uint64{2, 9, 2}, 3, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CommitIndex(tc.matches, tc.n); got != tc.want {
				t.Fatalf("CommitIndex(%v, %d) = %d, want %d",
					tc.matches, tc.n, got, tc.want)
			}
		})
	}
}

func TestCommitIndexDoesNotMutateInput(t *testing.T) {
	m := []uint64{3, 1, 2}
	CommitIndex(m, 3)
	if m[0] != 3 || m[1] != 1 || m[2] != 2 {
		t.Fatalf("input mutated: %v", m)
	}
}
