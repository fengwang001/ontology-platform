package quorum

import "testing"

func TestMajority(t *testing.T) {
	want := []int{0, 1, 2, 2, 3, 3, 4, 4}
	for n := 1; n <= 7; n++ {
		if got := Majority(n); got != want[n] {
			t.Errorf("Majority(%d) = %d, want %d", n, got, want[n])
		}
	}
	if got := Majority(0); got != 0 {
		t.Errorf("Majority(0) = %d, want 0", got)
	}
}

func TestCommitIndex(t *testing.T) {
	cases := []struct {
		name    string
		matches []uint64
		n       int
		want    uint64
	}{
		{"all matched", []uint64{5, 5, 5}, 3, 5},
		{"majority at 3, minority ahead", []uint64{3, 3, 9}, 3, 3},
		{"minority ahead never commits early", []uint64{1, 1, 100}, 3, 1},
		{"partial majority commits common prefix", []uint64{4, 2}, 3, 2},
		{"fewer than majority reporting", []uint64{4}, 3, 0},
		{"five nodes majority of three", []uint64{7, 7, 7, 2, 2}, 5, 7},
		{"five nodes only two matched", []uint64{9, 9, 1, 1, 1}, 5, 1},
		{"single node", []uint64{8}, 1, 8},
		{"empty matches", nil, 3, 0},
		{"unsorted input", []uint64{2, 9, 4, 4, 1}, 5, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CommitIndex(tc.matches, tc.n); got != tc.want {
				t.Errorf("CommitIndex(%v, %d) = %d, want %d",
					tc.matches, tc.n, got, tc.want)
			}
		})
	}
}

func TestCommitIndexDoesNotMutateInput(t *testing.T) {
	matches := []uint64{9, 1, 5}
	CommitIndex(matches, 3)
	if matches[0] != 9 || matches[1] != 1 || matches[2] != 5 {
		t.Fatalf("input mutated: %v", matches)
	}
}
