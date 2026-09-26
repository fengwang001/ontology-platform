package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/api"
	"ontology/lim"
	"ontology/tkn"
)

func ok(name string, good bool) {
	if good {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

type rq struct{ t, need int64 }

func main() {
	b := tkn.New(20, 2)
	line1 := b.TryConsume(15) && b.Tokens() == 5
	line1 = !b.TryConsume(8) && b.Tokens() == 5 && line1
	b.Refill(3)
	b.Refill(100)
	ok("tkn consume/refill/clamp", line1 && b.Tokens() == 20)

	// 2) 第三节八步序列，逐步打印判定与判定后 tokens。
	want := []rq{{0, 15}, {0, 8}, {3, 10}, {3, 5}, {8, 12}, {9, 6}, {20, 18}, {20, 3}}
	exp := []struct {
		a bool
		k int64
	}{
		{true, 5}, {false, 5}, {true, 1}, {false, 1},
		{false, 11}, {true, 7}, {true, 2}, {false, 2},
	}
	l := lim.New(20, 2)
	trace, good := "", true
	for i, w := range want {
		a, err := l.Allow(w.t, w.need)
		if err != nil || a != exp[i].a || l.Tokens() != exp[i].k {
			good = false
		}
		m := "F"
		if a {
			m = "T"
		}
		if i > 0 {
			trace += " "
		}
		trace += fmt.Sprintf("%s%d", m, l.Tokens())
	}
	ok("eight steps "+trace, good)

	// 3) 封顶生效：第 7 步仅当补令牌被 min 封顶为 20 时才 (true,2)。
	c, _ := api.New(20, 2)
	for _, r := range want[:6] {
		c.Allow(r.t, r.need)
	}
	a7, _ := c.Allow(20, 18)
	ok("refill clamped at capacity (step7 -> true,2)", a7 && c.Tokens() == 2)

	// 4) 令牌不越界 + 与朴素参照一致：随机单调序列，参照与实现在同一循环里逐步推进。
	boundsGood := true
	rng := rand.New(rand.NewSource(20260926))
	for iter := 0; iter < 200; iter++ {
		capacity, rate := int64(1+rng.Intn(50)), int64(1+rng.Intn(10))
		rs := make([]rq, 1+rng.Intn(30))
		var t int64
		for j := range rs {
			t += int64(rng.Intn(8))
			rs[j] = rq{t, int64(1 + rng.Intn(40))}
		}
		ll, _ := api.New(capacity, rate)
		ref, prevT := capacity, int64(0)
		for _, r := range rs {
			ref = min(capacity, ref+(r.t-prevT)*rate)
			prevT = r.t
			refWant := ref >= r.need
			if refWant {
				ref -= r.need
			}
			got, e := ll.Allow(r.t, r.need)
			tok := ll.Tokens()
			if e != nil || got != refWant || tok != ref || tok < 0 || tok > capacity {
				boundsGood = false
			}
		}
	}
	ok("bounds 0<=tokens<=cap and matches naive ref (200 random seqs)", boundsGood)

	// 5) 三类哨兵错误互不相同且可判定。
	_, eCfg := api.New(0, 2)
	le, _ := api.New(20, 2)
	_, eNeed := le.Allow(0, 0)
	le.Allow(5, 1) // 合法推进 last 到 5
	_, eBack := le.Allow(4, 1)
	distinct := errors.Is(eCfg, api.ErrInvalidConfig) && errors.Is(eNeed, api.ErrInvalidNeed) &&
		errors.Is(eBack, api.ErrClockRollback) && eCfg != eNeed && eNeed != eBack && eCfg != eBack
	ok("three distinct sentinel errors (config/need/rollback)", distinct)

	// 6) 被拒与非法调用均不留痕：复现 (乙)，第 5 步被拒后 tokens=11、last=8。
	d, _ := api.New(20, 2)
	for _, r := range want[:5] {
		d.Allow(r.t, r.need)
	}
	snap := d.Tokens() // 被拒不扣令牌：11
	_, en := d.Allow(8, 0)
	_, eb := d.Allow(7, 1)
	failedLeftState := d.Tokens() == snap // 非法调用后仍为 11
	after, _ := d.Allow(9, 6)             // 11+2=13，放行后 7
	ok("denied/failed calls leave no trace, limiter still usable",
		snap == 11 && errors.Is(en, api.ErrInvalidNeed) && errors.Is(eb, api.ErrClockRollback) &&
			failedLeftState && after && d.Tokens() == 7)

	// 7) 大 m 下补令牌为 O(1) 乘法：非导出 refillSteps 恒为 0，
	//    由 tkn 白盒测试 TestRefillIsO1 直接断言；此处从外部核对各档结果。
	mGood := true
	for _, m := range []int64{100, 1000, 5000, 10000} {
		lm, _ := api.New(m, 1)
		lm.Allow(0, m) // 抽干
		aa, _ := lm.Allow(m, 1)
		mGood = mGood && aa && lm.Tokens() == m-1
	}
	ok("O(1) refill at m=100..10000 (refillSteps==0 pinned by tkn test)", mGood)

	// 8) 并发：N 个 goroutine 在同一时间戳各消费 1，全部放行且恰好剩 capacity-N。
	const N = 32
	pc, _ := api.New(N, 1)
	var wg sync.WaitGroup
	results := make([]bool, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], _ = pc.Allow(10, 1)
		}(i)
	}
	wg.Wait()
	allAllow := true
	for _, r := range results {
		allAllow = allAllow && r
	}
	ok(fmt.Sprintf("concurrent %d goroutines all allowed, tokens==%d", N, pc.Tokens()),
		allAllow && pc.Tokens() == 0)
}
