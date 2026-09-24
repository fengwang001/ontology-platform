// Command demo 校验 as-of 时间旅行读关键语义，逐条打印 OK/FAIL，不读参数不联网，输出≤10 行。
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"ontology/api"
	"ontology/hist"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func view(m map[string]api.Val) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	ps := make([]string, 0, len(ks))
	for _, k := range ks {
		ps = append(ps, k+":"+strconv.Itoa(m[k].(int)))
	}
	return "{" + strings.Join(ps, " ") + "}"
}

var errNames = map[error]string{
	api.ErrCompacted: "ErrCompacted", api.ErrNegativeRead: "ErrNegativeRead",
	api.ErrEmptyKey: "ErrEmptyKey", api.ErrNegativeCompact: "ErrNegativeCompact",
}

func errName(e error) string {
	if e == nil {
		return "nil"
	}
	if n, ok := errNames[e]; ok {
		return n
	}
	return e.Error()
}

func newAB() *api.API { // W(a,1)=1 W(b,10)=2 W(a,2)=3
	a := api.New()
	a.Write("a", 1)
	a.Write("b", 10)
	a.Write("a", 2)
	return a
}

func main() {
	a := newAB()
	pre := make([]string, 5)
	for s := 0; s <= 4; s++ {
		v, _ := a.AsOf(s)
		pre[s] = view(v)
	}
	check("pre-compact AsOf(0..4)="+strings.Join(pre, " "),
		reflect.DeepEqual(pre, []string{"{}", "{a:1}", "{a:1 b:10}", "{a:2 b:10}", "{a:2 b:10}"}))

	a.Compact(2)
	_, e2 := a.AsOf(2)
	check("Compact(2) 后 AsOf(2)="+errName(e2), errors.Is(e2, api.ErrCompacted))

	v3, _ := a.AsOf(3)
	v4, _ := a.AsOf(4)
	wp := map[string]api.Val{"a": 2, "b": 10}
	check("Compact(2) 后 AsOf(3)="+view(v3)+" AsOf(4)="+view(v4)+"（b=10 基线仍在）",
		reflect.DeepEqual(v3, wp) && reflect.DeepEqual(v4, wp))

	c := newAB()
	c.Compact(3)
	cv, ce := c.AsOf(3)
	c4, _ := c.AsOf(4)
	check("Compact(3): a 基线="+strconv.Itoa(c4["a"].(int))+", AsOf(3)="+errName(ce)+"→"+view(cv)+", AsOf(4)="+view(c4),
		c4["a"] == 2 && errors.Is(ce, api.ErrCompacted) && reflect.DeepEqual(c4, wp))

	check("SelfCheck 与朴素重放一致、compact 不破坏可达读、不可达即报错", api.New().SelfCheck() == nil)
	x := api.New()
	_, eKey := x.Write("", 1)
	_, eNeg := x.AsOf(-1)
	x.Compact(0)
	_, eComp := x.AsOf(0)
	eUpto := x.Compact(-1)
	distinct := map[error]bool{}
	for _, e := range []error{api.ErrEmptyKey, api.ErrNegativeRead, api.ErrCompacted, api.ErrNegativeCompact} {
		distinct[e] = true
	}
	check("四类错误互不相同: "+errName(eKey)+" "+errName(eNeg)+" "+errName(eComp)+" "+errName(eUpto),
		errors.Is(eKey, api.ErrEmptyKey) && errors.Is(eNeg, api.ErrNegativeRead) &&
			errors.Is(eComp, api.ErrCompacted) && errors.Is(eUpto, api.ErrNegativeCompact) && len(distinct) == 4)
	y := api.New()
	y.Write("", 1)
	y.AsOf(-1)
	y.Compact(-1)
	seq, _ := y.Write("z", 7)
	y.Write("", 2)
	zv, _ := y.AsOf(1)
	check("被拒不留痕: 首个成功 seq="+strconv.Itoa(seq)+" MaxSeq="+strconv.Itoa(y.MaxSeq())+" view="+view(zv),
		seq == 1 && y.MaxSeq() == 1 && reflect.DeepEqual(zv, map[string]api.Val{"z": 7}))
	check("大 m 下检查个数为常数（经导出闸门，不读数值）", hist.QuickProbeOK() == nil)

	p := api.New()
	for i := 0; i < 400; i++ {
		p.Write([]string{"a", "b", "c"}[i%3], i)
	}
	reach := p.MaxSeq() / 2
	p.Compact(reach - 1)
	const N = 64
	var wg sync.WaitGroup
	views := make([]map[string]api.Val, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%5 == 0 {
				p.Write("late", i)
			}
			v, _ := p.AsOf(reach)
			views[i] = v
		}(g)
	}
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(views[i], views[0]) {
			same = false
		}
	}
	check("64 goroutine 并发只读同一可达位点逐 key 一致", same)

	if fails > 0 {
		panic("demo failed")
	}
}
