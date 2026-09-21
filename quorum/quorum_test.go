package quorum

import "testing"

func TestMajority(t *testing.T) {
	want := []int{0, 1, 2, 2, 3, 3, 4, 4} // index = n, for n = 0..7
	for n := 0; n <= 7; n++ {
		if got := Majority(n); got != want[n] {
			t.Errorf("Majority(%d) = %d, want %d", n, got, want[n])
		}
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
		{"exact majority", []uint64{7, 7, 2}, 3, 7},
		{"minority ahead cannot commit", []uint64{9, 1, 1}, 3, 1},
		{"no majority", []uint64{3, 0, 0}, 3, 0},
		{"five nodes", []uint64{4, 4, 4, 1, 1}, 5, 4},
		{"five nodes only two matched", []uint64{8, 8, 0, 0, 0}, 5, 0},
		{"single node", []uint64{6}, 1, 6},
		{"too few reports", []uint64{9, 9}, 5, 0},
		{"empty", nil, 3, 0},
		{"zero nodes", nil, 0, 0},
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
	in := []uint64{1, 9, 5}
	CommitIndex(in, 3)
	if in[0] != 1 || in[1] != 9 || in[2] != 5 {
		t.Fatalf("input mutated: %v", in)
	}
}
