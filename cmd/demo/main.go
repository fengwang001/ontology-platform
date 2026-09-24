// Command demo 校验多源并集增量去重的关键语义，逐条打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/src"
	"ontology/uni"
)

func report(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	s := src.New() // src 包幂等集合
	report("src-idempotent-set",
		s.Add("a") && !s.Add("a") && s.Remove("a") && !s.Remove("a") && s.Len() == 0,
		"dup-add/remove are no-op")

	v, _ := api.New(2) // 第三节八步：本地参考计数 cnt 与可观测 emit/View 逐步核对
	cnt := map[string]int{}
	hold := []map[string]struct{}{{}, {}}
	type st struct {
		p   int
		e   string
		add bool
	}
	steps := []st{{0, "a", true}, {1, "a", true}, {0, "b", true}, {0, "a", false},
		{1, "a", false}, {0, "b", false}, {1, "b", true}, {0, "b", false}}
	var b strings.Builder
	ok8, emit4, emit2, emit8 := true, true, true, true
	for i, o := range steps {
		before := len(v.Changes())
		var err error
		if o.add {
			err = v.Add(o.p, o.e)
		} else {
			err = v.Remove(o.p, o.e)
		}
		emit := len(v.Changes()) == before+1
		pre := cnt[o.e]
		if _, has := hold[o.p][o.e]; o.add && !has {
			hold[o.p][o.e] = struct{}{}
			cnt[o.e]++
		}
		if _, has := hold[o.p][o.e]; !o.add && has {
			delete(hold[o.p], o.e)
			cnt[o.e]--
		}
		wantEmit := (o.add && pre == 0) || (!o.add && cnt[o.e] == 0 && pre == 1)
		_ = err
		ok8 = ok8 && emit == wantEmit && reflect.DeepEqual(v.View(), unionOf(hold))
		if i == 3 {
			emit4 = !emit
		} // 步4 跨分区持有：cnt 2→1 不撤回
		if i == 1 {
			emit2 = !emit
		} // 步2 已在视图：不重复 +
		if i == 7 {
			emit8 = !emit
		} // 步8 不在分区0：不撤回
		ch := "·"
		if emit {
			ch = o.e
			if o.add {
				ch = "+" + ch
			} else {
				ch = "-" + ch
			}
		}
		fmt.Fprintf(&b, "%d:a%db%d/%s/%s ", i+1, cnt["a"], cnt["b"], ch, renderView(v.View()))
	}
	report("eight-steps-cnt-changelog-view", ok8, strings.TrimSpace(b.String()))
	report("step4-held-elsewhere-no-remove", emit4, "cnt a 2->1, no -a")
	report("step2-already-in-view-no-dup-plus", emit2, "cnt a 1->2, no +a")
	report("step8-not-in-partition-no-remove", emit8, "b held by p1, stays in view")

	report("view-equals-union-and-prefix-self-consistent", v.SelfCheck() == nil, "SelfCheck invariants 1-4")

	w2, _ := api.New(2) // 三类哨兵错误互不相同，且被拒后不留痕
	w2.Add(0, "z")
	bv, bc := w2.View(), w2.Changes()
	_, e0 := api.New(0)
	epn := w2.Add(9, "q")
	ee := w2.Add(0, "")
	distinct := errors.Is(e0, api.ErrBadNPart) && errors.Is(epn, api.ErrBadPart) &&
		errors.Is(ee, api.ErrEmptyElem) && e0 != epn && epn != ee
	notrace := reflect.DeepEqual(bv, w2.View()) && reflect.DeepEqual(bc, w2.Changes())
	report("three-sentinel-errors-and-no-trace", distinct && notrace, "badNPart/badPart/emptyElem")

	report("large-m-remove-check-o1", uni.ComplexityBoundOK(), "m=100/1000/10000 checks O(1)")

	wf, _ := api.New(8) // 并发只读：N goroutine 各取 View，逐字段一致，无 sleep
	for p := 0; p < 8; p++ {
		wf.Add(p, fmt.Sprintf("e%d", p%5))
	}
	var wg sync.WaitGroup
	views := make([]map[string]struct{}, 16)
	for g := range views {
		wg.Add(1)
		go func(g int) { defer wg.Done(); views[g] = wf.View() }(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < len(views); g++ {
		same = same && reflect.DeepEqual(views[0], views[g])
	}
	report("concurrent-readers-identical-view", same, "16 goroutines deep-equal")
}

func unionOf(parts []map[string]struct{}) map[string]struct{} {
	u := map[string]struct{}{}
	for _, s := range parts {
		for e := range s {
			u[e] = struct{}{}
		}
	}
	return u
}

func renderView(view map[string]struct{}) string {
	var elems []string
	for _, e := range []string{"a", "b"} {
		if _, ok := view[e]; ok {
			elems = append(elems, e)
		}
	}
	if len(elems) == 0 {
		return "{}"
	}
	return "{" + strings.Join(elems, "") + "}"
}
