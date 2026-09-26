package api

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/dfa"
	"ontology/nfa"
)

func TestAccepts(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"a", true}, {"ab", true}, {"ac", true}, {"abb", true}, {"abc", true},
		{"b", false}, {"", false}, {"z", false}, {"c", false}, {"acbb", false},
	}
	for _, tc := range cases {
		if got, err := BuildAndMatch(Example(), tc.s); err != nil || got != tc.want {
			t.Errorf("Accepts(%q)=%v,%v want %v", tc.s, got, err, tc.want)
		}
	}
}

// 不变量1：随机 NFA 上 dfa.Accepts 与参照模拟逐串相同。
func TestAcceptsMatchesSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(660))
	for round := 0; round < 50; round++ {
		ns := 1 + rng.Intn(5)
		alpha := []byte{'a', 'b', 'c'}[:1+rng.Intn(3)]
		n := &nfa.NFA{NumStates: ns, Trans: map[int]map[byte][]int{},
			Start: rng.Intn(ns), Accept: map[int]bool{rng.Intn(ns): true}, Alphabet: alpha}
		for i := 0; i < ns*3; i++ { // 随机字符转移与 ε 转移
			from, to := rng.Intn(ns), rng.Intn(ns)
			c := alpha[rng.Intn(len(alpha))]
			if rng.Intn(4) == 0 {
				c = nfa.Epsilon
			}
			if n.Trans[from] == nil {
				n.Trans[from] = map[byte][]int{}
			}
			n.Trans[from][c] = append(n.Trans[from][c], to)
		}
		d, err := dfa.Build(n)
		if err != nil {
			t.Fatal(err)
		}
		strs := enumerate(alpha, 5)
		for i := 0; i < 30; i++ { // 再补随机串（含字母表外字符）
			b := make([]byte, rng.Intn(8))
			for j := range b {
				b[j] = 'a' + byte(rng.Intn(5))
			}
			strs = append(strs, string(b))
		}
		for _, s := range strs {
			if d.Accepts(s) != simulate(n, s) {
				t.Fatalf("round %d: mismatch on %q", round, s)
			}
		}
	}
}

// 不变量3：同一 NFA 构造两次，状态集合与转移完全一致（确定性）。
func TestDeterministic(t *testing.T) {
	d1, err1 := dfa.Build(Example())
	d2, err2 := dfa.Build(Example())
	if err1 != nil || err2 != nil || d1.NumStates() != d2.NumStates() {
		t.Fatalf("build mismatch: %v %v", err1, err2)
	}
	for i := 0; i < d1.NumStates(); i++ {
		s1, s2 := d1.State(i), d2.State(i)
		if len(s1) != len(s2) || d1.IsAccept(i) != d2.IsAccept(i) {
			t.Fatalf("state %d differs", i)
		}
		for j := range s1 {
			if s1[j] != s2[j] {
				t.Fatalf("state %d set differs", i)
			}
		}
		for _, c := range Example().Alphabet {
			a, oka := d1.Next(i, c)
			b, okb := d2.Next(i, c)
			if oka != okb || a != b {
				t.Fatalf("transition (%d,%q) differs", i, c)
			}
		}
	}
}

// 不变量4：四类故障各有可判定哨兵错误且互不相同。
func TestFaultInjection(t *testing.T) {
	seen := map[error]bool{}
	for _, bc := range badCases() {
		ok, err := BuildAndMatch(bc.n, "a")
		if ok || !errors.Is(err, bc.want) {
			t.Errorf("got (%v,%v), want error %v", ok, err, bc.want)
		}
		if seen[bc.want] {
			t.Errorf("duplicate sentinel %v", bc.want)
		}
		seen[bc.want] = true
	}
	if len(seen) != 4 {
		t.Errorf("want 4 distinct sentinels, got %d", len(seen))
	}
}

// 不变量4：被拒后不产生可用 DFA，且不影响后续调用。
func TestFailureNoSideEffect(t *testing.T) {
	for _, bc := range badCases() {
		if _, err := dfa.Build(bc.n); err == nil {
			t.Fatal("invalid NFA produced a DFA")
		}
	}
	for _, tc := range []struct {
		s    string
		want bool
	}{{"a", true}, {"b", false}, {"abb", true}} {
		if got, err := BuildAndMatch(Example(), tc.s); err != nil || got != tc.want {
			t.Fatalf("after failures: Accepts(%q)=%v,%v", tc.s, got, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
