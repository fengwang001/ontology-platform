package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/uni"
)

var failed bool

func ok(name string, pass bool) {
	if !pass {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK " + name)
}
func sign(cs []api.Change) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = "+" + c.Elem
		if !c.Add {
			parts[i] = "-" + c.Elem
		}
	}
	return strings.Join(parts, " ")
}
func main() {
	e, _ := api.New(2)
	ops := []api.Op{
		{Add: true, P: 0, Elem: "a"}, {Add: true, P: 1, Elem: "a"},
		{Add: true, P: 0, Elem: "b"}, {Add: false, P: 0, Elem: "a"},
		{Add: false, P: 1, Elem: "a"}, {Add: false, P: 0, Elem: "b"},
		{Add: true, P: 1, Elem: "b"}, {Add: false, P: 0, Elem: "b"},
	}
	type row struct {
		ca, cb    int
		log, view string
	}
	rows := []row{
		{1, 0, "+a", "[a]"}, {2, 0, "+a", "[a]"}, {2, 1, "+a +b", "[a b]"},
		{1, 1, "+a +b", "[a b]"}, {0, 1, "+a +b -a", "[b]"}, {0, 0, "+a +b -a -b", "[]"},
		{0, 1, "+a +b -a -b +b", "[b]"}, {0, 1, "+a +b -a -b +b", "[b]"},
	}
	held := [2]map[string]bool{{}, {}}
	cnt := func(el string) int {
		n := 0
		for p := range held {
			if held[p][el] {
				n++
			}
		}
		return n
	}
	all, lens, a4 := true, []int{0}, false
	for i, o := range ops {
		_ = e.Apply([]api.Op{o})
		held[o.P][o.Elem] = o.Add
		r := rows[i]
		if cnt("a") != r.ca || cnt("b") != r.cb || sign(e.Changes()) != r.log ||
			fmt.Sprint(e.View()) != r.view {
			all = false
		}
		lens = append(lens, len(e.Changes()))
		if i == 3 {
			a4 = strings.Contains(fmt.Sprint(e.View()), "a")
		}
	}
	ok("八步序列: 逐步 cnt[a]/cnt[b]/changelog/View 与 NOTES 表一致", all)
	ok("第2步: 已在视图不重复 +", lens[2] == lens[1])
	ok("第4步: 跨分区持有不撤回", lens[4] == lens[3] && a4)
	ok("第8步: 不在分区不撤回", lens[8] == lens[7] && fmt.Sprint(e.View()) == "[b]")
	rng := rand.New(rand.NewSource(1))
	e2, _ := api.New(4)
	parts := [4]map[string]bool{{}, {}, {}, {}}
	replay := map[string]bool{}
	altOK, prev := true, 0
	for i := 0; i < 500; i++ {
		add := rng.Intn(2) == 0
		p, el := rng.Intn(4), fmt.Sprintf("e%d", rng.Intn(6))
		_ = e2.Apply([]api.Op{{Add: add, P: p, Elem: el}})
		parts[p][el] = add
		if cs := e2.Changes(); len(cs) > prev {
			ch := cs[len(cs)-1]
			if replay[ch.Elem] == ch.Add {
				altOK = false
			}
			replay[ch.Elem] = ch.Add
			prev = len(cs)
		}
	}
	union := []string{}
	for p := range parts {
		for el, has := range parts[p] {
			if has && !slices.Contains(union, el) {
				union = append(union, el)
			}
		}
	}
	slices.Sort(union)
	ok("View 与去重并集一致(随机对拍)", fmt.Sprint(e2.View()) == fmt.Sprint(union))
	ok("changelog 每个前缀自洽(严格交替)", altOK)
	e3, _ := api.New(2)
	_, errNew := api.New(0)
	sentinels := errors.Is(errNew, api.ErrInvalidNPart) &&
		errors.Is(e3.Add(-1, "a"), api.ErrPartitionOutOfRange) &&
		errors.Is(e3.Add(0, ""), api.ErrEmptyElement) &&
		!errors.Is(api.ErrEmptyElement, api.ErrPartitionOutOfRange)
	ok("三类可判定错误(哨兵互不相同)", sentinels)
	snap := fmt.Sprint(e3.View(), e3.Changes())
	noTrace := e3.Apply([]api.Op{{Add: true, P: 0, Elem: "q"}, {Add: true, P: 9, Elem: "r"}}) != nil &&
		e3.Add(0, "") != nil && e3.Remove(5, "x") != nil &&
		fmt.Sprint(e3.View(), e3.Changes()) == snap && e3.Add(1, "z") == nil
	ok("被拒后状态不变(单条与整批原子)", noTrace)
	probeOK := true
	for _, m := range []int{100, 1000, 10000} {
		probeOK = probeOK && uni.ProbeBounded(m)
	}
	ok("大 m 下检查个数不随 m 增长(m=100..10000)", probeOK)
	e4, _ := api.New(4)
	_ = e4.Apply([]api.Op{{Add: true, P: 0, Elem: "a"}, {Add: true, P: 1, Elem: "b"}})
	want := fmt.Sprint(e4.View())
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(16)
	views := make([]string, 16)
	for g := range views {
		go func(i int) { defer done.Done(); start.Wait(); views[i] = fmt.Sprint(e4.View()) }(g)
	}
	start.Done()
	done.Wait()
	same := true
	for _, v := range views {
		same = same && v == want
	}
	ok("并发只读 View 逐字段一致", same)
	if failed {
		os.Exit(1)
	}
}
