package fileref

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"ontology/snapchain"
)

// TestExpireAccessCountIndependentOfRetainedFiles pins the complexity rule:
// after snapshot1={a} and snapshot2={b0..b(m-1)} (a removed), Expire(1,
// MaxInt64) expires only snapshot1. Deletable files are decided by reference
// counts, so the visited snapshot+reference count must not grow with m:
// it is bounded by expired-snapshots + their referenced files + a constant.
func TestExpireAccessCountIndependentOfRetainedFiles(t *testing.T) {
	const constant = 2
	ms := []int{100, 500, 1000, 5000, 10000}
	var first int
	for i, m := range ms {
		ch := snapchain.New()
		st := New()
		ch.Commit(1, []string{"a"}, nil)
		st.Commit([]string{"a"}, nil)

		add := make([]string, m)
		for j := range add {
			add[j] = "b" + strconv.Itoa(j)
		}
		ch.Commit(2, add, []string{"a"})
		st.Commit(add, nil) // a is removed, so nothing is inherited

		del, err := st.Expire(ch, 1, math.MaxInt64)
		if err != nil {
			t.Fatalf("m=%d: expire: %v", m, err)
		}
		if len(del) != 1 || del[0] != "a" {
			t.Fatalf("m=%d: deleted %v, want [a]", m, del)
		}
		bound := 1 /*expired snaps*/ + 1 /*files of snapshot1*/ + constant
		if st.lastAccess > bound {
			t.Fatalf("m=%d: access count %d exceeds m-independent bound %d", m, st.lastAccess, bound)
		}
		if i == 0 {
			first = st.lastAccess
		} else if st.lastAccess != first {
			t.Fatalf("access count grew with m: %d then %d", first, st.lastAccess)
		}
	}
}

// TestCheckCommitNameConflicts covers every detectable name rejection.
func TestCheckCommitNameConflicts(t *testing.T) {
	cases := []struct {
		name        string
		seed        []string
		add, remove []string
		want        error
	}{
		{"duplicate in add", nil, []string{"x", "x"}, nil, ErrNameConflict},
		{"duplicate in remove", nil, nil, []string{"y", "y"}, ErrNameConflict},
		{"add intersects remove", nil, []string{"x"}, []string{"x"}, ErrNameConflict},
		{"reused committed name", []string{"x"}, []string{"x"}, nil, ErrNameConflict},
		{"valid fresh names", nil, []string{"a"}, []string{"b"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := New()
			if c.seed != nil {
				st.Commit(c.seed, nil)
			}
			err := st.CheckCommit(c.add, c.remove)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}
