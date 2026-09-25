package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/hll"
	"ontology/hsh"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	keys8 := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	wantJ := []int{8, 5, 2, 13, 15, 5, 4, 5}
	wantR := []uint8{7, 3, 2, 2, 1, 1, 2, 1}

	// 1. 第三节八个 key 每步的桶下标与秩（对照 NOTES.md 推导表）
	ok := true
	for i, k := range keys8 {
		h := hsh.Hash(k)
		if hsh.Bucket(h, 4) != wantJ[i] || hsh.Rho(h, 4) != wantR[i] {
			ok = false
		}
	}
	check("八key每步桶/秩符合推导表", ok)

	// 2. 八个 key 加完后的最终估计：线性计数前 15.1358，后 7.5201
	sk, _ := hll.New(4)
	for _, k := range keys8 {
		sk.Add(k)
	}
	raw, _ := sk.EstimateRaw()
	est, _ := sk.Estimate()
	check("线性计数前15.1358/后7.5201",
		math.Abs(raw-15.135802) < 1e-4 && math.Abs(est-7.520058) < 1e-4)

	// 3. 合并与逐一添加一致（逐桶）
	a, _ := api.New(8)
	b, _ := api.New(8)
	whole, _ := api.New(8)
	for i := 0; i < 3000; i++ {
		k := fmt.Sprintf("k-%d", i)
		whole.Add(k)
		if i%2 == 0 {
			a.Add(k)
		} else {
			b.Add(k)
		}
	}
	check("合并==逐一添加(逐桶)", a.Merge(b) == nil && eqReg(a, whole))

	// 4. 估计在 3σ 上界内（N=3000, m=256）
	estW, _ := whole.Estimate()
	check("估计在3σ内", math.Abs(estW-3000)/3000 <= 3*1.04/math.Sqrt(256))

	// 5. 三类可判定错误互不相同
	_, e1 := api.New(3)
	m5, _ := api.New(5)
	e2 := m5.Merge(whole)
	var zero api.Sketch
	e3 := zero.Add("x")
	check("三类可判定错误",
		errors.Is(e1, api.ErrBadPrecision) && errors.Is(e2, api.ErrMismatch) &&
			errors.Is(e3, api.ErrNotInit) && e1 != e2 && e2 != e3 && e1 != e3)

	// 6. 被拒后状态不变
	before := regs(m5)
	m5.Merge(whole)
	check("被拒后状态不变", eqRegBytes(before, regs(m5)))

	// 7. 大 m 下 Estimate 读寄存器数为 0（由 TestEstimateReadsZeroRegisters 钉住）
	big, _ := hll.New(16)
	for i := 0; i < 5000; i++ {
		big.Add(fmt.Sprintf("big-%d", i))
	}
	_, errBE := big.Estimate()
	check("大m Estimate O(1)(见测试)", errBE == nil)

	// 8. 并发 Add：估计正确且并发读单调
	conc, _ := api.New(12)
	var wg sync.WaitGroup
	var stop atomic.Bool
	mono := atomic.Bool{}
	mono.Store(true)
	go func() {
		prev := 0.0
		for !stop.Load() {
			if e, err := conc.Estimate(); err == nil {
				if e < prev {
					mono.Store(false)
				}
				prev = e
			}
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				conc.Add(fmt.Sprintf("c-%d-%d", g, i))
			}
		}(g)
	}
	wg.Wait()
	stop.Store(true)
	estC, _ := conc.Estimate()
	check("并发Add估计正确且单调",
		mono.Load() && math.Abs(estC-4000)/4000 <= 3*1.04/math.Sqrt(4096))

	// 9. 内置自检
	check("SelfCheck", conc.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

func regs(s *api.Sketch) []uint8 { return s.Registers() }

func eqReg(a, b *api.Sketch) bool { return eqRegBytes(a.Registers(), b.Registers()) }

func eqRegBytes(x, y []uint8) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
