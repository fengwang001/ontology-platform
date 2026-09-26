package api_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/dfa"
	"ontology/nfa"
)

func TestErrorInjection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*nfa.NFA)
		want   error
	}{
		{"start out of range", func(n *nfa.NFA) { n.Start = 9 }, nfa.ErrStartOutOfRange},
		{"transition out of range", func(n *nfa.NFA) { n.Trans[0]['a'] = []int{9} }, nfa.ErrTransitionOutOfRange},
		{"empty alphabet", func(n *nfa.NFA) { n.Alphabet = nil }, nfa.ErrEmptyAlphabet},
		{"empty accept set", func(n *nfa.NFA) { n.Accept = nil }, nfa.ErrNoAcceptStates},
	}
	for _, c := range cases {
		n := api.Section3NFA()
		c.mutate(n)
		if _, err := api.BuildAndMatch(n, "a"); !errors.Is(err, c.want) {
			t.Errorf("%s: got err=%v, want %v", c.name, err, c.want)
		}
		for _, o := range cases {
			if o.name != c.name && errors.Is(c.want, o.want) {
				t.Errorf("sentinel errors not distinct: %v vs %v", c.want, o.want)
			}
		}
	}
	// 被拒后不影响后续调用
	if ok, err := api.BuildAndMatch(api.Section3NFA(), "a"); err != nil || !ok {
		t.Errorf("valid call after rejections: ok=%v err=%v", ok, err)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// randomNFA 生成合法随机 NFA：状态 1..6 个，字母表非空子集，接受态非空。
func randomNFA(r *rand.Rand) *nfa.NFA {
	n := &nfa.NFA{N: 1 + r.Intn(6), Start: 0}
	for _, c := range []byte{'a', 'b', 'c'} {
		if r.Intn(2) == 0 || len(n.Alphabet) == 0 && c == 'c' {
			n.Alphabet = append(n.Alphabet, c)
		}
	}
	n.Trans = make([]map[byte][]int, n.N)
	for s := 0; s < n.N; s++ {
		for _, c := range append(append([]byte{}, n.Alphabet...), nfa.Epsilon) {
			if r.Intn(2) == 0 {
				if n.Trans[s] == nil {
					n.Trans[s] = map[byte][]int{}
				}
				n.Trans[s][c] = append(n.Trans[s][c], r.Intn(n.N))
			}
		}
	}
	for len(n.Accept) == 0 {
		if r.Intn(2) == 0 {
			n.Accept = append(n.Accept, r.Intn(n.N))
		}
	}
	return n
}

func TestEquivalenceVsSimulation(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		n := randomNFA(r)
		if err := n.Validate(); err != nil {
			t.Fatalf("trial %d: generated invalid NFA: %v", trial, err)
		}
		d := dfa.Build(n)
		for i := 0; i < 50; i++ {
			buf := make([]byte, r.Intn(7))
			for j := range buf {
				buf[j] = "abcz"[r.Intn(4)]
			}
			s := string(buf)
			if got, want := d.Accepts(s), api.Simulate(n, s); got != want {
				t.Fatalf("trial %d input %q: dfa=%v simulate=%v", trial, s, got, want)
			}
		}
	}
}
