package plan

import (
	"errors"
	"reflect"
	"testing"

	"ontology/name"
)

func seq(rs ...Req) []Req { return rs }

func TestBuildOrderAndCycles(t *testing.T) {
	cases := []struct {
		name   string
		init   []string
		reqs   []Req
		order  []Req // 期望拓扑序
		cycles int
	}{
		{"chain", []string{"a", "b"},
			seq(Req{"a", "b"}, Req{"b", "c"}),
			seq(Req{"b", "c"}, Req{"a", "b"}), 0},
		{"chain reversed input", []string{"a", "b"},
			seq(Req{"b", "c"}, Req{"a", "b"}),
			seq(Req{"b", "c"}, Req{"a", "b"}), 0},
		{"2-cycle", []string{"a", "b"},
			seq(Req{"a", "b"}, Req{"b", "a"}), nil, 1},
		{"3-cycle", []string{"a", "b", "c"},
			seq(Req{"a", "b"}, Req{"b", "c"}, Req{"c", "a"}), nil, 1},
		{"two disjoint cycles", []string{"a", "b", "c", "d"},
			seq(Req{"a", "b"}, Req{"b", "a"}, Req{"c", "d"}, Req{"d", "c"}),
			nil, 2},
		{"chain plus cycle", []string{"a", "b", "c", "d"},
			seq(Req{"a", "b"}, Req{"b", "a"}, Req{"c", "d"}, Req{"d", "e"}),
			seq(Req{"d", "e"}, Req{"c", "d"}), 1},
		{"single", []string{"a"}, seq(Req{"a", "z"}), seq(Req{"a", "z"}), 0},
		{"empty batch", []string{"a"}, nil, nil, 0},
		{"self loop no-op", []string{"a"}, seq(Req{"a", "a"}), nil, 0},
		{"empty-name names", []string{"", "x"}, seq(Req{"", "x"}, Req{"x", ""}), nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.init...)
			p, err := Build(ns, tc.reqs)
			if err != nil {
				t.Fatalf("unexpected conflict: %v", err)
			}
			if !reflect.DeepEqual(p.Order, tc.order) {
				t.Fatalf("order = %v, want %v", p.Order, tc.order)
			}
			if len(p.Cycles) != tc.cycles {
				t.Fatalf("cycles = %d, want %d", len(p.Cycles), tc.cycles)
			}
			if tc.name == "self loop no-op" && len(p.SelfLoops) != 1 {
				t.Fatalf("self loops = %v", p.SelfLoops)
			}
		})
	}
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		name string
		init []string
		reqs []Req
		kind error
	}{
		{"target exists", []string{"a", "x"}, seq(Req{"a", "x"}), ErrTargetExists},
		{"duplicate target", []string{"a", "b"}, seq(Req{"a", "x"}, Req{"b", "x"}), ErrDuplicateTarget},
		{"duplicate source", []string{"a"}, seq(Req{"a", "x"}, Req{"a", "y"}), ErrDuplicateSource},
		{"missing source", []string{"a"}, seq(Req{"z", "x"}), ErrMissingSource},
		{"target vacated is fine", []string{"a", "b"}, seq(Req{"a", "b"}, Req{"b", "x"}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.init...)
			before := ns.Snapshot()
			_, err := Build(ns, tc.reqs)
			if !errors.Is(err, tc.kind) {
				t.Fatalf("err = %v, want %v", err, tc.kind)
			}
			after := ns.Snapshot()
			if !name.EqualSet(before, after) {
				t.Fatalf("namespace changed during detection: %v -> %v", before, after)
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	base := []Req{
		{Old: "a", New: "p"}, {Old: "b", New: "q"},
		{Old: "c", New: "r"}, {Old: "d", New: "s"},
	}
	init := []string{"a", "b", "c", "d"}
	ns := name.New(init...)
	want, err := Build(ns, base)
	if err != nil {
		t.Fatal(err)
	}
	for shuffle := 0; shuffle < 20; shuffle++ {
		perm := make([]Req, len(base))
		for i, j := range randPerm(len(base), shuffle) {
			perm[i] = base[j]
		}
		ns2 := name.New(init...)
		got, err := Build(ns2, perm)
		if err != nil || !reflect.DeepEqual(got.Order, want.Order) {
			t.Fatalf("shuffle %d: order = %v, err = %v", shuffle, got, err)
		}
	}
}

// randPerm 是确定的置换（不引全局随机源）。
func randPerm(n, seed int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	x := seed*2654435761 + 1
	for i := n - 1; i > 0; i-- {
		x = x*1103515245 + 12345
		j := int(uint(x)>>16) % (i + 1)
		p[i], p[j] = p[j], p[i]
	}
	return p
}

func TestLookupBudget(t *testing.T) {
	const r = 50000
	init := make([]string, r)
	reqs := make([]Req, r)
	for i := 0; i < r; i++ {
		init[i] = nameN(i)
	}
	for i := 0; i < r; i++ {
		reqs[i] = Req{Old: nameN(i), New: nameN(i + r)}
	}
	ns := name.New(init...)
	p, err := Build(ns, reqs)
	if err != nil {
		t.Fatal(err)
	}
	names := 2 * r // 涉及名字数（旧名 + 新名）
	budget := 4 * (r + names)
	if p.Lookups() > budget {
		t.Fatalf("lookups = %d, budget = %d", p.Lookups(), budget)
	}
}

func nameN(i int) string {
	return "n" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
