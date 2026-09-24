package ljoin

import (
	"errors"
	"fmt"
	"testing"

	"ontology/jstate"
)

func chg(s jstate.Side, op byte, id, k string) Change {
	return Change{Side: s, Op: op, ID: id, Key: k}
}

func TestNineStepOutputs(t *testing.T) {
	steps := []Change{
		chg(jstate.SideR, '+', "r1", "x"),
		chg(jstate.SideL, '+', "l1", "x"),
		chg(jstate.SideL, '+', "l2", "y"),
		chg(jstate.SideL, '+', "l3", "y"),
		chg(jstate.SideR, '+', "r2", "y"),
		chg(jstate.SideR, '+', "r3", "y"),
		chg(jstate.SideR, '-', "r2", ""),
		chg(jstate.SideR, '-', "r3", ""),
		chg(jstate.SideR, '-', "r1", ""),
	}
	want := [][]Out{
		{},
		{{'+', "l1", "r1"}},
		{{'+', "l2", ""}},
		{{'+', "l3", ""}},
		{{'-', "l2", ""}, {'+', "l2", "r2"}, {'-', "l3", ""}, {'+', "l3", "r2"}},
		{{'+', "l2", "r3"}, {'+', "l3", "r3"}},
		{{'-', "l2", "r2"}, {'-', "l3", "r2"}},
		{{'-', "l2", "r3"}, {'+', "l2", ""}, {'-', "l3", "r3"}, {'+', "l3", ""}},
		{{'-', "l1", "r1"}, {'+', "l1", ""}},
	}
	e := New(0)
	for i, st := range steps {
		got, err := e.Apply([]Change{st})
		if err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if !outsEqual(got, want[i]) {
			t.Errorf("step %d: got %v want %v", i+1, got, want[i])
		}
	}
}

func TestCheckedRowsComplexity(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e := New(0)
		for i := 0; i < m; i++ {
			k := fmt.Sprintf("k%05d", i)
			if _, err := e.applyOne(e.t, chg(jstate.SideL, '+', "l"+k, k)); err != nil {
				t.Fatal(err)
			}
			if _, err := e.applyOne(e.t, chg(jstate.SideR, '+', "r"+k, k)); err != nil {
				t.Fatal(err)
			}
		}
		o1, err := e.applyOne(e.t, chg(jstate.SideL, '+', "lz", "z"))
		if err != nil {
			t.Fatal(err)
		}
		c1 := e.checked
		o2, err := e.applyOne(e.t, chg(jstate.SideR, '+', "rz", "z"))
		if err != nil {
			t.Fatal(err)
		}
		// 检查个数只与本键行数有关，必须与 m 无关：常数 2 + 本步输出条数即可覆盖。
		if c1 > 2+len(o1) || e.checked > 2+len(o2) {
			t.Errorf("m=%d: checked %d/%d outs %d/%d grows with m", m, c1, e.checked, len(o1), len(o2))
		}
		if c1 != 0 || e.checked != 1 {
			t.Errorf("m=%d: want checked 0 then 1, got %d then %d", m, c1, e.checked)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	cases := []struct {
		name  string
		max   int
		pre   []Change
		batch []Change
		want  error
	}{
		{"invalid-empty-id", 0, nil, []Change{chg(jstate.SideL, '+', "", "k")}, ErrInvalidChange},
		{"invalid-empty-key", 0, nil, []Change{chg(jstate.SideR, '+', "r", "")}, ErrInvalidChange},
		{"invalid-delete-empty-id", 0, nil, []Change{chg(jstate.SideL, '-', "", "ignored")}, ErrInvalidChange},
		{"duplicate-l", 0, []Change{chg(jstate.SideL, '+', "a", "k")}, []Change{chg(jstate.SideL, '+', "a", "k")}, ErrDuplicateID},
		{"duplicate-r", 0, []Change{chg(jstate.SideR, '+', "r", "k")}, []Change{chg(jstate.SideR, '+', "r", "k")}, ErrDuplicateID},
		{"missing-l", 0, nil, []Change{chg(jstate.SideL, '-', "ghost", "ignored")}, ErrMissingID},
		{"missing-r", 0, nil, []Change{chg(jstate.SideR, '-', "ghost", "ignored")}, ErrMissingID},
		{"too-many", 1, nil, []Change{chg(jstate.SideL, '+', "a", "k"), chg(jstate.SideR, '+', "b", "k")}, ErrTooManyRows},
	}
	seen := map[error]bool{}
	for _, tc := range cases {
		e := New(tc.max)
		if _, err := e.Apply(tc.pre); err != nil {
			t.Fatalf("%s: prep: %v", tc.name, err)
		}
		before := e.t.Total()
		_, err := e.Apply(tc.batch)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, err, tc.want)
		}
		seen[tc.want] = true
		if e.t.Total() != before {
			t.Errorf("%s: rejected batch left a trace (%d != %d)", tc.name, e.t.Total(), before)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct sentinel errors, got %d", len(seen))
	}
}

func TestRejectedBatchStillUsable(t *testing.T) {
	e := New(0)
	if _, err := e.Apply([]Change{chg(jstate.SideL, '+', "a", "k")}); err != nil {
		t.Fatal(err)
	}
	_, err := e.Apply([]Change{
		chg(jstate.SideR, '+', "b", "k"),
		chg(jstate.SideL, '+', "a", "k"), // 重复 ID，整批回滚
	})
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("got %v want ErrDuplicateID", err)
	}
	if e.t.Has(jstate.SideR, "b") {
		t.Fatal("rejected batch changed state")
	}
	if _, err := e.Apply([]Change{chg(jstate.SideR, '+', "b", "k")}); err != nil {
		t.Fatalf("engine not usable after rejection: %v", err)
	}
}

func outsEqual(a, b []Out) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
