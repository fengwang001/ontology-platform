package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/dfa"
	"ontology/nfa"
)

var failed bool

func ok(name string, cond bool) {
	mark := "OK"
	if !cond {
		mark, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", name, mark)
}

func fmtSet(s []int) string {
	if len(s) == 0 {
		return "∅"
	}
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = string(rune('0' + v))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func main() {
	// api：四类故障注入，各自得到可判定且互不相同的哨兵错误
	faults := []struct {
		mutate func(*nfa.NFA)
		want   error
	}{
		{func(n *nfa.NFA) { n.Start = 9 }, nfa.ErrStartOutOfRange},
		{func(n *nfa.NFA) { n.Trans[0]['a'] = []int{9} }, nfa.ErrTransitionOutOfRange},
		{func(n *nfa.NFA) { n.Alphabet = nil }, nfa.ErrEmptyAlphabet},
		{func(n *nfa.NFA) { n.Accept = nil }, nfa.ErrNoAcceptStates},
	}
	injOK := true
	for i, f := range faults {
		n := api.Section3NFA()
		f.mutate(n)
		_, err := api.BuildAndMatch(n, "a")
		injOK = injOK && errors.Is(err, f.want)
		for j := i + 1; j < len(faults); j++ {
			injOK = injOK && !errors.Is(faults[j].want, f.want)
		}
	}
	ok("errors: 4 faults -> 4 distinct sentinels", injOK)

	// dfa：第三节八步分步表（每个 DFA 状态一行，含各字符后继与接受标记）
	d := dfa.Build(api.Section3NFA())
	wantSteps := []string{
		"steps A={0}: a->{1,2}* b->∅ c->∅",
		"steps B={1,2}*: a->∅ b->{1,2}* c->{2}*",
		"steps C={2}*: a->∅ b->∅ c->{2}*",
	}
	star := map[bool]string{true: "*", false: ""}
	for i, want := range wantSteps {
		var b strings.Builder
		fmt.Fprintf(&b, "steps %c=%s%s:", 'A'+i, fmtSet(d.States[i]), star[d.Accept[i]])
		for _, c := range []byte{'a', 'b', 'c'} {
			if nxt, has := d.Trans[i][c]; has {
				fmt.Fprintf(&b, " %c->%s%s", c, fmtSet(d.States[nxt]), star[d.Accept[nxt]])
			} else {
				fmt.Fprintf(&b, " %c->∅", c)
			}
		}
		ok(b.String(), b.String() == want)
	}

	// dfa：Accepts 判定
	inputs := []string{"a", "ab", "ac", "abb", "b", ""}
	wantAcc := []bool{true, true, true, true, false, false}
	got := make([]bool, len(inputs))
	var desc strings.Builder
	for i, s := range inputs {
		got[i] = d.Accepts(s)
		fmt.Fprintf(&desc, " %q=%v", s, got[i])
	}
	ok("accepts:"+desc.String(), fmt.Sprint(got) == fmt.Sprint(wantAcc))

	// api：被拒后状态不变，后续调用不受影响
	acc, err := api.BuildAndMatch(api.Section3NFA(), "abb")
	ok("post-reject: state unchanged", err == nil && acc && d.Accepts("abb"))

	// dfa：查重检查个数不随 m 增长（哈希定位）
	ok("dedup: checks const for m=100..10000", dfa.VerifyDedupScaling())

	// dfa：并发 Accepts 结果逐值相同
	const G = 32
	results := make([][]bool, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := make([]bool, len(inputs))
			for i, s := range inputs {
				r[i] = d.Accepts(s)
			}
			results[g] = r
		}(g)
	}
	wg.Wait()
	same := true
	for g := range results {
		same = same && fmt.Sprint(results[g]) == fmt.Sprint(got)
	}
	ok("concurrent: 32 goroutines identical", same)

	// api：四条不变量自检
	ok("SelfCheck", api.SelfCheck() == nil)

	if failed {
		fmt.Println("FAIL")
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
