package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dfa"
	"ontology/nfa"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	n := api.Example()
	d, err := dfa.Build(n)
	check("build", err == nil && d.NumStates() == 3)

	// 第三节分步表：3 状态 × 3 字符，逐格核验（仅列出的边存在，其余无转移）
	edges := map[int]int{ // from<<8|char -> to
		(0<<8 | 'a'): 1, (1<<8 | 'b'): 1, (1<<8 | 'c'): 2, (2<<8 | 'c'): 2,
	}
	stepsOK := true
	for i := 0; i < d.NumStates(); i++ {
		for _, c := range n.Alphabet {
			to, ok := d.Next(i, c)
			wantTo, wantOk := edges[i<<8|int(c)]
			stepsOK = stepsOK && ok == wantOk && (!ok || to == wantTo)
		}
	}
	check("steps A={0} B={1,2}* C={2}* edges A-a>B B-b>B B-c>C C-c>C", stepsOK &&
		!d.IsAccept(0) && d.IsAccept(1) && d.IsAccept(2))

	// 示例 DFA 的接受结果
	accOK := true
	for _, tc := range []struct {
		s    string
		want bool
	}{{"a", true}, {"ab", true}, {"ac", true}, {"abb", true}, {"b", false}, {"", false}} {
		accOK = accOK && d.Accepts(tc.s) == tc.want
	}
	check(`accepts a=T ab=T ac=T abb=T b=F ""=F`, accOK)

	// 四类可判定错误，互不相同
	faults := []struct {
		n    *nfa.NFA
		want error
	}{
		{&nfa.NFA{NumStates: 3, Trans: n.Trans, Start: 7, Accept: n.Accept, Alphabet: n.Alphabet}, nfa.ErrStartOutOfRange},
		{&nfa.NFA{NumStates: 3, Trans: map[int]map[byte][]int{0: {'a': {9}}}, Start: 0, Accept: n.Accept, Alphabet: n.Alphabet}, nfa.ErrBadTarget},
		{&nfa.NFA{NumStates: 3, Trans: n.Trans, Start: 0, Accept: n.Accept}, nfa.ErrEmptyAlphabet},
		{&nfa.NFA{NumStates: 3, Trans: n.Trans, Start: 0, Alphabet: n.Alphabet}, nfa.ErrNoAccept},
	}
	faultOK, seen := true, map[error]bool{}
	for _, f := range faults {
		ok, err := api.BuildAndMatch(f.n, "a")
		faultOK = faultOK && !ok && errors.Is(err, f.want) && !seen[f.want]
		seen[f.want] = true
	}
	check("faults start/target/alphabet/accept distinct", faultOK && len(seen) == 4)

	// 被拒后已有 DFA 状态不变
	check("state intact after rejects", d.NumStates() == 3 && d.Accepts("a") && !d.Accepts("b"))

	// 大 m 下查重检查个数不随 m 增长
	check("dedup checks O(1) for m=100..10000", dfa.VerifyDedupScales())

	// 并发接受结果逐值一致
	strs := []string{"a", "ab", "ac", "abb", "b", "", "z", "abc", "acbb"}
	want := make([]bool, len(strs))
	for i, s := range strs {
		want[i] = d.Accepts(s)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	concOK := true
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, s := range strs {
				if d.Accepts(s) != want[i] {
					mu.Lock()
					concOK = false
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent accepts identical", concOK)

	check("selfcheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
