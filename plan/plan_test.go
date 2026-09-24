package plan

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/name"
)

// runSteps 在命名空间副本上逐步执行，返回每步目标名是否预先不存在、最终集合。
func runSteps(t *testing.T, init []string, steps []Step) (bool, []string) {
	t.Helper()
	ns := name.New(init)
	ns.Lock()
	defer ns.Unlock()
	clean := true
	for _, s := range steps {
		if ns.HasLocked(s.To) {
			clean = false
		}
		if err := ns.RenameLocked(s.From, s.To, false); err != nil {
			t.Fatalf("step %v failed: %v", s, err)
		}
	}
	return clean, ns.SnapshotLocked()
}

func stepStrings(ss []Step) string {
	out := ""
	for _, s := range ss {
		out += fmt.Sprintf("(%s->%s)", s.From, s.To)
	}
	return out
}

func TestOrderingAndCycles(t *testing.T) {
	cases := []struct {
		name      string
		init      []string
		reqs      []Req
		wantSeq   string
		wantTemps int
		wantFinal []string
	}{
		{"empty", nil, nil, "", 0, nil},
		{"single", []string{"a"}, []Req{{"a", "b"}}, "(a->b)", 0, []string{"b"}},
		{"self loop noop", []string{"a"}, []Req{{"a", "a"}}, "", 0, []string{"a"}},
		{"chain a-b b-c", []string{"a", "b"},
			[]Req{{"a", "b"}, {"b", "c"}}, "(b->c)(a->b)", 0, []string{"b", "c"}},
	}
	cases = append(cases, []struct {
		name      string
		init      []string
		reqs      []Req
		wantSeq   string
		wantTemps int
		wantFinal []string
	}{
		{"2-cycle", []string{"a", "b"}, []Req{{"a", "b"}, {"b", "a"}},
			"(a->.rename-tmp-0)(b->a)(.rename-tmp-0->b)", 1, []string{"a", "b"}},
		{"3-cycle", []string{"a", "b", "c"},
			[]Req{{"a", "b"}, {"b", "c"}, {"c", "a"}},
			"(a->.rename-tmp-0)(c->a)(b->c)(.rename-tmp-0->b)", 1,
			[]string{"a", "b", "c"}},
		{"empty string names", []string{"", "x"}, []Req{{"", "y"}, {"x", ""}},
			"(->y)(x->)", 0, []string{"", "y"}},
	}...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.init)
			ns.Lock()
			defer ns.Unlock()
			steps, st, err := Compile(ns, tc.reqs)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if got := stepStrings(steps); got != tc.wantSeq {
				t.Fatalf("seq=%s want %s", got, tc.wantSeq)
			}
			if st.TempNames != tc.wantTemps {
				t.Fatalf("temps=%d want %d", st.TempNames, tc.wantTemps)
			}
			clean, final := runSteps(t, tc.init, steps)
			if !clean {
				t.Fatal("a step targeted an existing name (overwrite)")
			}
			if !name.Equal(final, tc.wantFinal) {
				t.Fatalf("final=%v want %v", final, tc.wantFinal)
			}
		})
	}
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		name    string
		init    []string
		reqs    []Req
		wantErr error
	}{
		{"target exists", []string{"a", "b"}, []Req{{"a", "b"}}, ErrTargetExists},
		{"dup target", []string{"a", "b"}, []Req{{"a", "c"}, {"b", "c"}}, ErrDupTarget},
		{"dup source", []string{"a"}, []Req{{"a", "b"}, {"a", "c"}}, ErrDupSource},
		{"missing source", []string{"a"}, []Req{{"x", "y"}}, ErrMissingSource},
		{"many dup source blocked", []string{"a"},
			[]Req{{"a", "1"}, {"a", "2"}, {"a", "3"}}, ErrDupSource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.init)
			ns.Lock()
			defer ns.Unlock()
			before := ns.SnapshotLocked()
			_, _, err := Compile(ns, tc.reqs)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if got := ns.SnapshotLocked(); !name.Equal(got, before) {
				t.Fatalf("namespace changed after conflict: %v vs %v", got, before)
			}
		})
	}
}

func TestComplexityBound(t *testing.T) {
	const r = 50000
	var init []string
	var rs []Req
	for i := 0; i < r; i++ {
		from := fmt.Sprintf("src%05d", i)
		to := fmt.Sprintf("dst%05d", i)
		init = append(init, from)
		rs = append(rs, Req{from, to})
	}
	ns := name.New(init)
	ns.Lock()
	defer ns.Unlock()
	_, st, err := Compile(ns, rs)
	if err != nil {
		t.Fatal(err)
	}
	bound := 4 * (r + st.Names)
	if st.Lookups > bound {
		t.Fatalf("lookups=%d > bound %d", st.Lookups, bound)
	}
}

func TestTempCountEqualsCycles(t *testing.T) {
	var init []string
	var rs []Req
	for c := 0; c < 10; c++ {
		a := fmt.Sprintf("a%d", c)
		b := fmt.Sprintf("b%d", c)
		init = append(init, a, b)
		rs = append(rs, Req{a, b}, Req{b, a})
	}
	ns := name.New(init)
	ns.Lock()
	defer ns.Unlock()
	_, st, err := Compile(ns, rs)
	if err != nil {
		t.Fatal(err)
	}
	if st.TempNames != 10 {
		t.Fatalf("temps=%d want 10", st.TempNames)
	}
}

func TestDeterminism(t *testing.T) {
	var init []string
	var rs []Req
	for i := 0; i < 6; i++ {
		a := fmt.Sprintf("a%d", i)
		b := fmt.Sprintf("b%d", i)
		init = append(init, a, b)
		rs = append(rs, Req{a, b}, Req{b, a}) // 多个环覆盖排序分支
	}
	init = append(init, "s1", "s2", "s3")
	rs = append(rs, Req{"s1", "t1"}, Req{"s2", "t2"}, Req{"s3", "t3"})
	var ref string
	for round := 0; round < 20; round++ {
		shuffled := append([]Req(nil), rs...)
		rng := rand.New(rand.NewSource(int64(round)))
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		ns := name.New(init)
		ns.Lock()
		steps, _, err := Compile(ns, shuffled)
		ns.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		got := stepStrings(steps)
		if round == 0 {
			ref = got
		} else if got != ref {
			t.Fatalf("round %d sequence differs:\n%s\n%s", round, got, ref)
		}
	}
}
