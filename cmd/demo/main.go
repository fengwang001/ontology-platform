// 演示程序：逐项打印 OK/FAIL，任一 FAIL 则退出码非 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/broker"
	"ontology/seqstate"
)

var failed bool

func report(name string, ok bool) {
	s := "OK  "
	if !ok {
		s, failed = "FAIL ", true
	}
	fmt.Println(s + name)
}

func main() {
	checkSteps()
	report("随机序列与朴素参照一致", api.New(4, 3).SelfCheck() == nil)
	checkFencingErrors()
	checkNoTrace()
	checkLargeM()
	checkConcurrent()
	if failed {
		os.Exit(1)
	}
}

func checkSteps() {
	st := seqstate.New(2)
	st.Accept(0, 10)
	st.Accept(1, 11)
	st.Accept(2, 12) // 窗口 {1@11, 2@12}
	_, d1, e1 := st.Judge(1)
	_, d2, e2 := st.Judge(0)
	_, d3, e3 := st.Judge(4)
	_, d4, e4 := st.Judge(3)
	ok1 := d1 && e1 == nil && !d2 && errors.Is(e2, seqstate.ErrDuplicateExpired) && !d3 && errors.Is(e3, seqstate.ErrOutOfOrder) && !d4 && e4 == nil
	b := broker.New(2, 2)
	b.Produce(7, 0, 0, 0, 0)
	b.Produce(7, 0, 0, 1, 0)
	_, _, be1 := b.Produce(7, 0, 2, 0, 0) // 分区越界
	b.Produce(7, 1, 1, 0, 0)              // 升级，惰性清空
	_, _, be2 := b.Produce(7, 0, 0, 2, 0) // 围栏
	bo, _, be3 := b.Produce(7, 1, 0, 0, 0)
	ok2 := errors.Is(be1, broker.ErrInvalid) && errors.Is(be2, broker.ErrFenced) && be3 == nil && bo == 2 && len(b.Log(0)) == 3 && len(b.Log(1)) == 1
	report("seqstate/broker 逐步判定", ok1 && ok2)
	type exp struct {
		off int
		dup bool
		err error
	}
	reqs := [][3]int{{0, 0, 0}, {0, 1, 0}, {0, 0, 1}, {0, 0, 1}, {0, 0, 3}, {0, 0, 2}, {0, 0, 0}, {1, 1, 0}, {0, 0, 3}, {1, 0, 0}}
	exps := []exp{{0, false, nil}, {0, false, nil}, {1, false, nil}, {1, true, nil}, {0, false, api.ErrOutOfOrder},
		{2, false, nil}, {0, false, api.ErrDuplicateExpired}, {1, false, nil}, {0, false, api.ErrFenced}, {3, false, nil}}
	s := api.New(2, 2)
	ok := true
	for i, r := range reqs {
		off, dup, err := s.Produce(7, r[0], r[1], r[2], 0)
		ok = ok && off == exps[i].off && dup == exps[i].dup && errors.Is(err, exps[i].err) && (err == nil) == (exps[i].err == nil)
	}
	report("十步判定与返回位点", ok)
	report("两分区最终日志", fmt.Sprint(s.Log(0)) == "[{7 0 0 0} {7 0 1 0} {7 0 2 0} {7 1 0 0}]" && fmt.Sprint(s.Log(1)) == "[{7 0 0 0} {7 1 0 0}]")
}

func checkFencingErrors() {
	s := api.New(2, 2)
	s.Produce(1, 1, 0, 0, 0)
	_, _, ferr := s.Produce(1, 0, 0, 0, 0)
	report("围栏后旧 epoch 不落盘", errors.Is(ferr, api.ErrFenced) && len(s.Log(0)) == 1)
	s.Produce(1, 1, 0, 1, 0)
	s.Produce(1, 1, 0, 2, 0) // 窗口 {1,2}
	_, _, e1 := s.Produce(-1, 1, 0, 0, 0)
	_, _, e2 := s.Produce(1, 0, 0, 0, 0)
	_, _, e3 := s.Produce(1, 1, 0, 5, 0)
	_, _, e4 := s.Produce(1, 1, 0, 0, 0)
	ok := errors.Is(e1, api.ErrInvalid) && errors.Is(e2, api.ErrFenced) && errors.Is(e3, api.ErrOutOfOrder) && errors.Is(e4, api.ErrDuplicateExpired)
	ok = ok && api.ErrInvalid != api.ErrFenced && api.ErrInvalid != api.ErrOutOfOrder && api.ErrInvalid != api.ErrDuplicateExpired &&
		api.ErrFenced != api.ErrOutOfOrder && api.ErrFenced != api.ErrDuplicateExpired && api.ErrOutOfOrder != api.ErrDuplicateExpired
	report("四类可判定错误互不相同", ok)
}

func checkNoTrace() {
	s := api.New(2, 2)
	s.Produce(5, 0, 0, 0, 0)
	s.Produce(5, 1, 1, 0, 0)
	b0, b1 := s.Log(0), s.Log(1)
	s.Produce(-1, 0, 0, 0, 0) // 非法
	s.Produce(5, 0, 0, 0, 0)  // 围栏
	s.Produce(5, 1, 0, 7, 0)  // 乱序
	s.Produce(5, 9, 0, 3, 0)  // 升级但 seq≠0：不得升级 epoch
	ok := reflect.DeepEqual(s.Log(0), b0) && reflect.DeepEqual(s.Log(1), b1)
	off, _, err := s.Produce(5, 2, 0, 0, 0)
	report("被拒后状态不变(含epoch未升级)", ok && err == nil && off == 1)
}

func checkLargeM() {
	const m = 10000
	s := api.New(m, 4)
	for p := 0; p < m; p++ {
		s.Produce(1, 0, p, 0, 0)
	}
	_, _, err1 := s.Produce(1, 1, 0, 0, 0)
	_, _, err2 := s.Produce(1, 1, m-1, 0, 0)
	_, _, err3 := s.Produce(1, 1, 1, 1, 0)
	ok := err1 == nil && err2 == nil && errors.Is(err3, api.ErrOutOfOrder)
	report("大m下升级后行为正确(计数不随m增长的证明见broker测试)", ok)
}

func checkConcurrent() {
	const K, S, W = 8, 32, 64
	s := api.New(1, W)
	offs := make([][]int, K)
	var wg sync.WaitGroup
	for g := 0; g < K; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			offs[g] = make([]int, S)
			for seq := 0; seq < S; seq++ {
				off, _, err := s.Produce(9, 0, 0, seq, seq)
				for err != nil {
					off, _, err = s.Produce(9, 0, 0, seq, seq)
				}
				offs[g][seq] = off
			}
		}(g)
	}
	wg.Wait()
	lg := s.Log(0)
	ok := len(lg) == S
	for i, rec := range lg {
		ok = ok && rec.Seq == i
	}
	for g := 0; g < K; g++ {
		for seq := 0; seq < S; seq++ {
			ok = ok && offs[g][seq] == seq
		}
	}
	report("并发重复发送恰好一次落盘", ok)
}
