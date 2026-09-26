package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/est"
)

var failed bool

func mark(ok bool) string {
	failed = failed || !ok
	return map[bool]string{true: "OK", false: "FAIL"}[ok]
}

func check(name string, ok bool) {
	fmt.Printf("%s %s\n", name, mark(ok))
}

func main() {
	seq := [][2]int{{0, 0}, {2, 2}, {2, 4}, {5, 1}, {0, 3}, {5, 5}, {2, 3}}
	wantRegs := [][8]int{{1, 0, 0, 0, 0, 0, 0, 0}, {1, 0, 3, 0, 0, 0, 0, 0}, {1, 0, 5, 0, 0, 0, 0, 0},
		{1, 0, 5, 0, 0, 2, 0, 0}, {4, 0, 5, 0, 0, 2, 0, 0}, {4, 0, 5, 0, 0, 6, 0, 0}, {4, 0, 5, 0, 0, 6, 0, 0}}

	// 第三节：七步每步之后的寄存器数组。
	s, _ := api.New(8)
	stepsOK, trace := true, ""
	for i, p := range seq {
		if s.Add(p[0], p[1]) != nil {
			stepsOK = false
		}
		for j, v := range s.Registers() {
			trace += string(rune('0' + v))
			stepsOK = stepsOK && v == wantRegs[i][j]
		}
		trace += "|"
	}
	fmt.Printf("steps %s %s\n", mark(stepsOK), trace)

	// Estimate 的值：α_8·8·2^(15/8)。
	estOK := s.Estimate() == est.Alpha(8)*8*math.Exp2(15.0/8.0)
	fmt.Printf("estimate %s %.4f\n", mark(estOK), s.Estimate())

	// 单调不减：随机序列逐 Add 校验。
	rng := rand.New(rand.NewSource(1))
	mono, _ := api.New(16)
	prev, monoOK := mono.Registers(), true
	for i := 0; i < 2000; i++ {
		if mono.Add(rng.Intn(16), rng.Intn(20)) != nil {
			monoOK = false
		}
		cur := mono.Registers()
		for j := range cur {
			monoOK = monoOK && cur[j] >= prev[j]
		}
		prev = cur
	}
	check("monotonic", monoOK)

	// 单元素精确。
	one, _ := api.New(8)
	_ = one.Add(3, 2)
	singleOK := true
	for j, v := range one.Registers() {
		singleOK = singleOK && (j == 3) == (v == 3)
	}
	check("single-exact", singleOK)

	// 与朴素重放一致（随机序列）。
	rep, _ := api.New(16)
	naive := make([]int, 16)
	for i := 0; i < 500; i++ {
		b, z := rng.Intn(16), rng.Intn(30)
		_ = rep.Add(b, z)
		if z+1 > naive[b] {
			naive[b] = z + 1
		}
	}
	got, sum, repOK := rep.Registers(), 0, true
	for j := range got {
		repOK = repOK && got[j] == naive[j]
		sum += naive[j]
	}
	repOK = repOK && rep.Estimate() == est.Alpha(16)*16*math.Exp2(float64(sum)/16)
	check("naive-replay", repOK)

	// 三类可判定错误，互不相同。
	errOK := true
	for _, m := range []int{0, -2, 3, 6} {
		_, err := api.New(m)
		errOK = errOK && errors.Is(err, api.ErrInvalidM)
	}
	errOK = errOK && errors.Is(s.Add(-1, 0), api.ErrBucketRange) &&
		errors.Is(s.Add(8, 0), api.ErrBucketRange) && errors.Is(s.Add(0, -1), api.ErrNegativeZ) &&
		api.ErrInvalidM != api.ErrBucketRange && api.ErrBucketRange != api.ErrNegativeZ && api.ErrInvalidM != api.ErrNegativeZ
	check("distinct-errors", errOK)

	// 被拒后状态不变，且之后仍可正常使用。
	before := s.Registers()
	_ = s.Add(-1, 0)
	_ = s.Add(8, 0)
	_ = s.Add(0, -1)
	rejOK := true
	for j, v := range s.Registers() {
		rejOK = rejOK && v == before[j]
	}
	rejOK = rejOK && s.Add(1, 1) == nil && s.Registers()[1] == 2
	check("rejected-unchanged", rejOK)

	// 大 N 下 Estimate 访问寄存器数恒等于 m；并发估计结果一致。
	bigOK := true
	for _, n := range []int{100, 1000, 10000} {
		sk, _ := api.New(16)
		for i := 0; i < n; i++ {
			_ = sk.Add(rng.Intn(16), rng.Intn(40))
		}
		_ = sk.Estimate()
		bigOK = bigOK && sk.LastEstimateReadAll()
	}
	const G = 16
	results := make([]float64, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			_ = s.Registers()
			_ = s.SelfCheck()
			results[g] = s.Estimate()
		}(g)
	}
	wg.Wait()
	concOK := true
	for _, v := range results {
		concOK = concOK && v == results[0]
	}
	check("bign-reads-m concurrent-estimate", bigOK && concOK)

	check("selfcheck", s.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
