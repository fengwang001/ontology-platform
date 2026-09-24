package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/rule"
)

func ops12(s *api.System) []func() { // 第三节的十二个操作
	return []func(){
		func() { _ = s.Publish(rule.Put("r1", 10)) }, func() { _ = s.Deliver(0, 1) }, func() { _ = s.Send(0, 15) }, func() { _ = s.Send(1, 12) }, func() { _ = s.Publish(rule.Put("r2", 5)) }, func() { _ = s.Publish(rule.Delete("r1")) },
		func() { _ = s.Send(1, 8) }, func() { _ = s.Send(0, 20) }, func() { _ = s.Deliver(1, 3) }, func() { _ = s.Deliver(0, 1) }, func() { _ = s.Send(2, 7) }, func() { _ = s.Deliver(0, 1) },
	}
}

var want12 = []string{ // 十二步每步之后的 "G 各v_i buf0 buf1 累计命中数"
	"1 [0 0] [] [] 0", "1 [1 0] [] [] 0", "1 [1 0] [] [] 1", "1 [1 0] [] [1] 1", "2 [1 0] [] [1] 1", "3 [1 0] [] [1] 1",
	"3 [1 0] [] [1 3] 1", "3 [1 0] [3] [1 3] 1", "3 [1 3] [3] [] 3", "3 [2 3] [3] [] 3", "3 [2 3] [3 3] [] 3", "3 [3 3] [] [] 5",
}

func checkTwelveSteps() (ok, snap9 bool) {
	s := api.New(2, 8)
	for i, f := range ops12(s) {
		f()
		if fmt.Sprint(s.Versions())+" "+fmt.Sprint(s.BufTags(0), s.BufTags(1), len(s.Output())) != want12[i] {
			return false, false
		}
	}
	const expHits = "[{0 15 r1 1 0} {1 12 r1 1 1} {1 8 r2 3 1} {0 20 r2 3 0} {2 7 r2 3 0}]"
	ok = fmt.Sprint(s.Output()) == expHits
	return ok, ok // exp[1]=(1,12,r1,1,1)：tag=1 用 v1 规则集 {r1}，即第 9 步按 tag 快照的判定
}

func runRandom(r *rand.Rand) (*api.System, []rule.Update, []rule.Data) {
	s := api.New(3, 100)
	var log []rule.Update
	var data []rule.Data
	flush := func() {
		g, vs := s.Versions()
		for i, v := range vs {
			if v < g {
				_ = s.Deliver(i, g-v)
			}
		}
	}
	for i := 0; i < 20; i++ {
		if i%3 == 0 {
			if u := rule.Put(fmt.Sprintf("r%d", i%4), int64(i%7)); s.Publish(u) == nil {
				log = append(log, u)
			}
		} else if key, val := int64(i), int64(i*5%17); s.Send(key, val) == nil {
			data = append(data, rule.Data{Key: key, Val: val, Tag: len(log), Inst: int(key % 3)})
		}
		if r.Intn(2) == 0 {
			flush()
		}
	}
	flush()
	return s, log, data
}

func checkErrors() (decidable, noTrace bool) {
	s := api.New(2, 1)
	_ = s.Publish(rule.Put("r1", 10))
	_ = s.Send(0, 1) // 占满实例 0 缓冲
	before := fmt.Sprint(s.Versions()) + fmt.Sprint(s.Output(), s.BufTags(0))
	bads := []error{s.Publish(rule.Put("", 1)), s.Publish(rule.Delete("x")), s.Deliver(0, 2), s.Send(-1, 1), s.Send(2, 1)}
	kinds := []error{rule.ErrEmptyID, rule.ErrNoSuch, api.ErrDeliver, api.ErrBadKey, api.ErrBufFull}
	decidable = true
	for i, got := range bads {
		for j, k := range kinds {
			if (i == j) != errors.Is(got, k) { // 每个错误恰好匹配自己的类别
				decidable = false
			}
		}
	}
	return decidable, fmt.Sprint(s.Versions())+fmt.Sprint(s.Output(), s.BufTags(0)) == before
}

func checkLargeM() bool {
	s := api.New(1, 10000)
	for i := 0; i < 10005; i++ {
		if i < 5 {
			_ = s.Publish(rule.Put("r", 0))
		} else if s.Send(int64(i-5), 3) != nil {
			return false
		}
	}
	if s.Deliver(0, 4) != nil || len(s.Output()) != 0 { // 不到 G，不刷出
		return false
	}
	return s.Deliver(0, 1) == nil && len(s.Output()) == 10000 // 到 G，刷出 m 条
}

func checkConcurrent() bool {
	s := api.New(4, 500)
	var log []rule.Update
	for i := 0; i < 8; i++ {
		u := rule.Put(fmt.Sprintf("r%d", i), int64(i))
		_ = s.Publish(u)
		log = append(log, u)
	}
	var data []rule.Data
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = s.Deliver(i, 8) }(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 160; i++ {
			if s.Send(int64(i%4), int64(i%9)) == nil {
				data = append(data, rule.Data{Key: int64(i % 4), Val: int64(i % 9), Tag: 8, Inst: i % 4})
			}
		}
	}()
	wg.Wait()
	return rule.SameHits(s.Output(), rule.Naive(log, data))
}

func main() {
	fails := 0
	report := func(name string, ok bool) {
		if !ok {
			fails++
		}
		fmt.Println(name, map[bool]string{true: "OK", false: "FAIL"}[ok])
	}
	ok12, snap9 := checkTwelveSteps()
	report("十二步版本/缓冲/命中", ok12)
	report("第9步按tag快照处理", snap9)
	a, logA, dataA := runRandom(rand.New(rand.NewSource(1)))
	b, _, _ := runRandom(rand.New(rand.NewSource(2)))
	report("随机投递穿插命中集合不变", rule.SameHits(a.Output(), b.Output()))
	report("与朴素参照一致", rule.SameHits(a.Output(), rule.Naive(logA, dataA)))
	dec, noTrace := checkErrors()
	report("四类可判定错误", dec)
	report("被拒后状态不变", noTrace)
	report("大m刷出正确", checkLargeM())
	report("并发投递与发送一致", checkConcurrent())
	if fails > 0 {
		os.Exit(1)
	}
}
