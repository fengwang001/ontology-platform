package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/es"
	"ontology/replay"
)

var failed bool

func check(name string, got, want any) {
	if reflect.DeepEqual(got, want) {
		fmt.Println("OK  " + name)
	} else {
		failed = true
		fmt.Printf("FAIL %s: got %v want %v\n", name, got, want)
	}
}

// wrong 模拟错误实现：从 base 起按给定事件原样顺序应用（不跳过、不排序）。
func wrong(base int64, evs ...es.Event) int64 {
	for _, ev := range evs {
		base = es.Apply(base, ev)
	}
	return base
}

func main() {
	evs := []es.Event{{Seq: 1, Delta: 10}, {Seq: 2, Delta: -3}, {Seq: 3, Delta: 7},
		{Seq: 4, Delta: -20}, {Seq: 5, Delta: 2}}
	s := api.New()
	bal, steps := int64(0), make([]int64, 0, 5)
	for _, ev := range evs {
		_ = s.Append(ev)
		bal = es.Apply(bal, ev)
		steps = append(steps, bal)
	}
	check("五步表 10/7/14/0/2", steps, []int64{10, 7, 14, 0, 2})
	snap := es.Snapshot{Seq: 3, Total: 14}
	got, err := s.Replay(snap, evs)
	check("快照后重放=2", []any{got, err}, []any{int64(2), nil})
	check("(甲)(乙)(丙)错值=3/6/0", []int64{
		wrong(14, evs[2], evs[3], evs[4]), // 甲：seq3 重复应用
		wrong(14, evs[0], evs[3], evs[4]), // 乙：旧事件 seq1 不跳过
		wrong(14, evs[4], evs[3]),         // 丙：乱序 seq5,seq4
	}, []int64{3, 6, 0})
	again, _ := s.Replay(snap, evs)
	withOld, _ := s.Replay(snap, append([]es.Event{evs[0]}, evs...))
	check("幂等(重复重放/旧事件跳过)", []int64{got, again, withOld}, []int64{2, 2, 2})
	onlyOld, _ := s.Replay(snap, evs[:3])
	check("快照边界(Seq<=3不再应用)", onlyOld, int64(14))
	e1 := s.Append(es.Event{Seq: 0, Delta: 1})
	_, e2 := s.Replay(es.Snapshot{Seq: -1}, nil)
	_, e3 := s.Replay(snap, []es.Event{{Seq: 2, Delta: 1}, {Seq: 1, Delta: 1}})
	check("三类可判定错误", []bool{
		errors.Is(e1, es.ErrBadEvent), errors.Is(e2, es.ErrBadSnapshot),
		errors.Is(e3, replay.ErrBadReplay)}, []bool{true, true, true})
	check("被拒后状态不变", s.State(), es.Snapshot{Seq: 5, Total: 2})
	big := make([]es.Event, 10000)
	for i := range big {
		big[i] = es.Event{Seq: int64(i + 1), Delta: 1}
	}
	_, err = s.Replay(es.Snapshot{Seq: 5000, Total: 5000}, big)
	check("大m定位O(log m)(TestLocateSublinear钉住)", err, nil)
	const n = 32
	var wg sync.WaitGroup
	res := make([]es.Snapshot, n)
	for i := range res {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = s.State() }(i)
	}
	wg.Wait()
	same := true
	for _, r := range res {
		same = same && r == res[0]
	}
	check("并发只读一致+SelfCheck", []bool{same, s.SelfCheck() == nil}, []bool{true, true})
	if failed {
		os.Exit(1)
	}
}
