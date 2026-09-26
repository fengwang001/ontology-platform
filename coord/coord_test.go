package coord

import (
	"errors"
	"testing"

	"ontology/pt"
)

// naive 朴素重算：逐个扫描全部参与者，全 yes 才 Commit，否则 Abort。
func naive(votes []pt.Vote) Decision {
	for _, v := range votes {
		if v != pt.Yes {
			return Abort
		}
	}
	return Commit
}

// 不变量1：任意投票序列后 Decide 与朴素重算一致。
func TestDecideMatchesNaive(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5, 8, 17} {
		c := New(n)
		votes := make([]pt.Vote, n)
		seed := uint32(n*2654435761 + 7)
		for step := 0; step < 5*n; step++ {
			seed = seed*1664525 + 1013904223
			p := int(seed>>16) % n
			yes := seed>>31 == 0
			if c.Vote(p, yes) == nil {
				if yes {
					votes[p] = pt.Yes
				} else {
					votes[p] = pt.No
				}
			}
		}
		for p := range votes { // 补满未投的参与者
			if votes[p] == pt.Unvoted {
				if err := c.Vote(p, true); err == nil {
					votes[p] = pt.Yes
				}
			}
		}
		if got, err := c.Decide(); err != nil || got != naive(votes) {
			t.Fatalf("n=%d: Decide=%v,%v naive=%v", n, got, err, naive(votes))
		}
	}
}

// 不变量2：Commit 当且仅当全体 yes。
func TestUnanimousOnly(t *testing.T) {
	for _, n := range []int{1, 2, 3, 10} {
		for skip := -1; skip < n; skip++ { // skip=-1 全 yes；否则位置 skip 投 no
			c := New(n)
			for p := 0; p < n; p++ {
				if err := c.Vote(p, p != skip); err != nil {
					t.Fatalf("n=%d skip=%d: %v", n, skip, err)
				}
			}
			got, err := c.Decide()
			if err != nil {
				t.Fatalf("n=%d skip=%d: %v", n, skip, err)
			}
			want := Commit
			if skip >= 0 {
				want = Abort
			}
			if got != want {
				t.Fatalf("n=%d skip=%d: got %v want %v", n, skip, got, want)
			}
		}
	}
}

// 不变量3：票一旦投出不可更改。
func TestVoteImmutable(t *testing.T) {
	c := New(2)
	if err := c.Vote(0, false); err != nil {
		t.Fatal(err)
	}
	if err := c.Vote(0, true); !errors.Is(err, pt.ErrDuplicateVote) {
		t.Fatalf("recast err=%v", err)
	}
	if err := c.Vote(1, true); err != nil {
		t.Fatal(err)
	}
	if d, _ := c.Decide(); d != Abort {
		t.Fatalf("vote mutated: decide=%v", d)
	}
}

// 不变量4：被拒绝的操作不改变任何状态，且之后仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	c := New(2)
	if err := c.Vote(0, true); err != nil {
		t.Fatal(err)
	}
	y0, n0 := c.Counts()
	if err := c.Vote(-1, true); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("oob low err=%v", err)
	}
	if err := c.Vote(2, true); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("oob high err=%v", err)
	}
	if err := c.Vote(0, false); !errors.Is(err, pt.ErrDuplicateVote) {
		t.Fatalf("dup err=%v", err)
	}
	if _, err := c.Decide(); !errors.Is(err, ErrNotAllVoted) {
		t.Fatalf("early decide err=%v", err)
	}
	if y, n := c.Counts(); y != y0 || n != n0 {
		t.Fatalf("state changed: (%d,%d)->(%d,%d)", y0, n0, y, n)
	}
	if err := c.Vote(1, true); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
	if d, _ := c.Decide(); d != Commit {
		t.Fatalf("decide=%v", d)
	}
}
