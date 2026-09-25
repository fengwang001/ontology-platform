// Command demo 验证 ontology-454 谓词下推实现。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/pipe"
	"ontology/pred"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func sixEvents() []pred.Event {
	return []pred.Event{
		{Seq: 1, Val: 10, Kind: "A"}, {Seq: 2, Val: 25, Kind: "A"},
		{Seq: 3, Val: 18, Kind: "A"}, {Seq: 4, Val: 30, Kind: "B"},
		{Seq: 5, Val: 22, Kind: "A"}, {Seq: 6, Val: 40, Kind: "A"},
	}
}

func vals(evs []pred.Event) []int64 {
	out := make([]int64, len(evs))
	for i, e := range evs {
		out[i] = e.Val
	}
	return out
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkNaiveChain 用 pred 手工搭 F1→S→F2 朴素链，核验六步输出与第 4 步 max。
func checkNaiveChain() {
	f1, f2, s := pred.Even(), pred.KindEq("A"), pred.NewHighWater()
	var got []int64
	maxAfter4 := int64(0)
	for i, e := range sixEvents() {
		if f1.Eval(e) && s.Eval(e) && f2.Eval(e) {
			got = append(got, e.Val)
		}
		if i == 3 {
			maxAfter4 = s.Max()
		}
	}
	check("六步输出=10,18,40 且第4步后 max=30", eq(got, []int64{10, 18, 40}) && maxAfter4 == 30)
}

// chain 构造 F1→S→F2 算子链。
func chain() []pipe.Op {
	return []pipe.Op{pipe.Filter(pred.Even()), pipe.RecordHigh(), pipe.Filter(pred.KindEq("A"))}
}

// checkPipe 核验：合并后输出与朴素链一致；跨屏障移动被拒；大 m 合并为单 AND。
func checkPipe() {
	plan, err := pipe.Build(chain())
	naive := pipe.RunNaive(chain(), sixEvents())
	check("段内合并后输出与朴素链一致", err == nil && eq(vals(plan.Run(sixEvents())), vals(naive)))

	_, err = pipe.Build(chain(), pipe.Move{From: 2, To: 0}) // F2 移到 S 之上
	check("跨越屏障的下推请求被拒(ErrBarrier)", errors.Is(err, pipe.ErrBarrier))

	for _, m := range []int{100, 1000, 10000} {
		ops := make([]pipe.Op, m)
		for i := range ops {
			ops[i] = pipe.Filter(pred.Even())
		}
		p, err := pipe.Build(ops)
		if err != nil || p.Len() != 1 {
			check("大m合并为单AND谓词", false)
			return
		}
	}
	check("大m(100~10000)合并为单AND谓词", true)
}

// checkAPI 核验：三类可判定错误互不相同、被拒后状态不变、并发只读一致、自检通过。
func checkAPI() {
	_, errPred := api.New([]api.Op{pipe.Filter(pred.OfField(99, ""))})
	pl, err := api.New(chain())
	if err != nil {
		check("api 构建", false)
		return
	}
	if _, err = pl.Feed(sixEvents()); err != nil {
		check("api 喂入", false)
		return
	}
	before := pl.Output()
	_, errEvent := pl.Feed([]api.Event{{Seq: 0, Val: 1, Kind: "x"}})
	_, errEvent2 := pl.Feed([]api.Event{{Seq: 9, Val: 1, Kind: ""}})
	_, errBarrier := pipe.Build(chain(), pipe.Move{From: 2, To: 0})
	distinct := !errors.Is(errPred, api.ErrBadEvent) && !errors.Is(errPred, api.ErrBarrier) &&
		!errors.Is(errEvent, api.ErrBadPredicate) && !errors.Is(errBarrier, api.ErrBadPredicate)
	check("三类错误可判定且互不相同",
		errors.Is(errPred, api.ErrBadPredicate) && errors.Is(errEvent, api.ErrBadEvent) &&
			errors.Is(errEvent2, api.ErrBadEvent) && errors.Is(errBarrier, api.ErrBarrier) && distinct)
	check("被拒后状态与输出不变", eq(vals(pl.Output()), vals(before)))

	const n = 8
	outs := make([][]api.Out, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i] = pl.Output()
			_ = api.SelfCheck()
		}()
	}
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && eq(vals(outs[i]), vals(outs[0]))
	}
	check("并发只读结果逐字段相同", same)
	check("SelfCheck 四不变量", api.SelfCheck() == nil)
}

func main() {
	checkNaiveChain()
	checkPipe()
	checkAPI()
	if failed {
		os.Exit(1)
	}
}
