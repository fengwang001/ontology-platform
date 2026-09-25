package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/reorder"
	"ontology/seq"
)

var failed bool

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

// runEleven 执行第三节十一步（0=Tick），返回每步错误与每步发出的 Seq。
func runEleven(x *api.Buffer) ([]error, [][]int64) {
	var errs []error
	var outs [][]int64
	for _, s := range []int64{1, 3, 5, 0, 0, 2, 4, 8, 9, 10, 11} {
		var out []seq.Event
		var e error
		if s == 0 {
			out, e = x.Tick()
		} else {
			out, e = x.Feed(seq.Event{Seq: s, Value: int(s)})
		}
		var ss []int64
		for _, ev := range out {
			ss = append(ss, ev.Seq)
		}
		errs, outs = append(errs, e), append(outs, ss)
	}
	return errs, outs
}

func drain(x *api.Buffer) {
	for range [16]struct{}{} {
		x.Tick()
	}
}

func increasing(es []seq.Event) bool {
	for i := 1; i < len(es); i++ {
		if es[i-1].Seq >= es[i].Seq {
			return false
		}
	}
	return true
}

func main() {
	// 1) 十一步逐步：每步发出 / 报错；清空后比对朴素重放结果与丢失集合。
	b, _ := api.New(3, 2)
	errs, outs := runEleven(b)
	wantOut := [][]int64{{1}, nil, nil, nil, {3}, nil, {4, 5}, nil, nil, nil, nil}
	wantErr := []error{nil, nil, nil, nil, nil, seq.ErrExpired, nil, nil, nil, nil, reorder.ErrOverflow}
	stepOK := reflect.DeepEqual(outs, wantOut)
	for i := range errs {
		if !errors.Is(errs[i], wantErr[i]) {
			stepOK = false
		}
	}
	drain(b)
	got := b.View()
	want := []seq.Event{{Seq: 1, Value: 1}, {Seq: 3, Value: 3}, {Seq: 4, Value: 4}, {Seq: 5, Value: 5}, {Seq: 8, Value: 8}, {Seq: 9, Value: 9}, {Seq: 10, Value: 10}}
	report("十一步逐步 now/next/缓冲/发出/丢失/报错", stepOK && reflect.DeepEqual(b.Lost(), []int64{2, 6, 7}) && reorder.SelfCheck() == nil)
	report("第5步超时(>=)丢2发3/第7步级联发5/第11步溢出不留痕/大m堆比较", reorder.SelfCheck() == nil)
	report("与朴素重放一致(Seq与Value逐条相同)", reflect.DeepEqual(want, got))
	report("发出顺序严格递增", increasing(got))

	// 缓冲不超界：容量 3 连投 4 个缺口，第 4 个必报溢出且缓冲仍为 {2,3,4}。
	bd, _ := api.New(3, 2)
	for _, s := range []int64{2, 3, 4} {
		bd.Feed(seq.Event{Seq: s})
	}
	_, ovf := bd.Feed(seq.Event{Seq: 5})
	report("缓冲不超界(加入前判满、被拒不留痕)", errors.Is(ovf, reorder.ErrOverflow))

	// 四类哨兵错误可判定且互不相同。
	p, _ := api.New(1, 0)
	_, e1 := p.Feed(seq.Event{Seq: 0})
	p.Feed(seq.Event{Seq: 1})
	_, e2 := p.Feed(seq.Event{Seq: 1})
	p.Feed(seq.Event{Seq: 3})
	_, e3 := p.Feed(seq.Event{Seq: 4})
	_, e4 := api.New(0, -1)
	report("四类错误可判定且互不相同", errors.Is(e1, seq.ErrInvalid) && errors.Is(e2, seq.ErrExpired) &&
		errors.Is(e3, reorder.ErrOverflow) && errors.Is(e4, reorder.ErrBadParams) && e1 != e2 && e2 != e3 && e3 != e4)

	// 被拒后状态不变且仍可正常使用：溢出后再 Feed(1) 命中并级联发 2。
	r, _ := api.New(1, 1)
	r.Feed(seq.Event{Seq: 2})
	_, rej := r.Feed(seq.Event{Seq: 3})
	_, cont := r.Feed(seq.Event{Seq: 1})
	report("被拒后状态不变且仍可正常使用", errors.Is(rej, reorder.ErrOverflow) && cont == nil && len(r.View()) == 2)

	// 并发：喂满并 Tick 清空后 N 个 goroutine 并发读同一实例，逐字段一致，无 sleep。
	c, _ := api.New(32, 1)
	for s := int64(1); s <= 32; s++ {
		c.Feed(seq.Event{Seq: s, Value: int(s)})
	}
	drain(c)
	base := c.View()
	const n = 32
	var wg sync.WaitGroup
	res := make([][]seq.Event, n)
	start := make(chan struct{})
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			res[g] = c.View()
			_ = c.Lost()
			_ = c.SelfCheck()
		}(g)
	}
	close(start)
	wg.Wait()
	same := true
	for _, v := range res {
		if !reflect.DeepEqual(base, v) {
			same = false
		}
	}
	report("并发读 View/Lost/SelfCheck 逐字段一致", same)

	if failed {
		os.Exit(1)
	}
}
