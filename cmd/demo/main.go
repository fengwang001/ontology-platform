package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/pipe"
	"ontology/pred"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	e := pred.Event{Seq: 1, Val: 10, Kind: "A"}
	ok("pred: 谓词求值与非法事件可判定", pred.Even().Eval(e) &&
		errors.Is(pred.Event{Seq: 0, Kind: "A"}.Valid(), pred.ErrBadEvent))

	// 第三节：链 F1→S→F2，六事件；第 4 步后 S.max 应为 30，输出 {10,18,40}
	s := &pred.RecordHigh{}
	ops := []pipe.Op{
		{Name: "F1", Pred: pred.Even()},
		{Name: "S", State: s},
		{Name: "F2", Pred: pred.KindIs("A")},
	}
	pl, err := new(pipe.Planner).Build(ops)
	if err != nil {
		fmt.Println("FAIL pipe: Build:", err)
		return
	}
	evs := []pred.Event{
		{Seq: 1, Val: 10, Kind: "A"}, {Seq: 2, Val: 25, Kind: "A"},
		{Seq: 3, Val: 18, Kind: "A"}, {Seq: 4, Val: 30, Kind: "B"},
		{Seq: 5, Val: 22, Kind: "A"}, {Seq: 6, Val: 40, Kind: "A"},
	}
	head, err := pl.Feed(evs[:4])
	m4, _ := s.Max()
	tail, err2 := pl.Feed(evs[4:])
	got := []int64{}
	for _, o := range append(head, tail...) {
		got = append(got, o.Val)
	}
	ok("pipe: 六步输出{10,18,40} 且第4步后 S.max=30",
		err == nil && err2 == nil && fmt.Sprint(got) == "[10 18 40]" && m4 == 30)
	ok("pipe: 跨屏障移动被拒", errors.Is(pipe.ValidateMove(ops, 2, 0), pipe.ErrBarrier) &&
		errors.Is(pipe.ValidateMove(ops, 0, 2), pipe.ErrBarrier))

	ok("api: SelfCheck 四条不变量", api.SelfCheck() == nil)

	// 三类可判定错误互不相同
	_, errPred := api.New([]api.Op{{Name: "bad", Pred: pred.Pred{Name: "nil"}}})
	inst, _ := api.New([]api.Op{{Name: "S", State: &pred.RecordHigh{}}})
	_, errEv := inst.Feed([]api.Event{{Seq: -1, Kind: "A"}})
	errBar := pipe.ValidateMove(ops, 2, 0)
	ok("api: 三类错误可判定且互不相同",
		errors.Is(errPred, pred.ErrBadPredicate) &&
			errors.Is(errEv, pred.ErrBadEvent) &&
			errors.Is(errBar, pipe.ErrBarrier) &&
			!errors.Is(errPred, pred.ErrBadEvent) && !errors.Is(errEv, pipe.ErrBarrier))

	// 被拒后状态不变：非法批次里的 Val=100 不得进入 S.max
	before := inst.Out()
	_, err = inst.Feed([]api.Event{{Seq: 1, Val: 10, Kind: "A"}, {Seq: 0, Val: 100, Kind: "A"}})
	g, err2 := inst.Feed([]api.Event{{Seq: 2, Val: 50, Kind: "A"}})
	ok("api: 被拒批次不留痕", err != nil && err2 == nil && len(g) == 1 && before == nil)

	// 大 m：10000 个无状态过滤器合并为一个 AND，结果与朴素链一致（线性证明见 pipe 测试）
	big := make([]api.Op, 10000)
	for i := range big {
		i := i
		big[i] = api.Op{Name: fmt.Sprint(i), Pred: pred.Pred{Name: fmt.Sprint(i), Field: "Val",
			F: func(ev pred.Event) bool { return ev.Val >= int64(i%3) }}}
	}
	bigA, errA := api.New(big)
	bigN, errN := pipe.Naive(big)
	var eq bool
	if errA == nil && errN == nil {
		ga, _ := bigA.Feed(evs)
		gn, _ := bigN.Feed(evs)
		eq = fmt.Sprint(ga) == fmt.Sprint(gn)
	}
	ok("pipe: 大 m 合并正确（线性检查次数由测试钉住）", eq)

	// 并发只读：N 个 goroutine 读同一实例，输出逐字段相同
	full, _ := api.New([]api.Op{{Name: "S", State: &pred.RecordHigh{}}})
	want, _ := full.Feed(evs)
	var wg sync.WaitGroup
	var same atomic.Bool
	same.Store(true)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fmt.Sprint(full.Out()) != fmt.Sprint(want) || api.SelfCheck() != nil {
				same.Store(false)
			}
		}()
	}
	wg.Wait()
	ok("api: 并发只读结果一致", same.Load())

	if failed {
		os.Exit(1)
	}
}
