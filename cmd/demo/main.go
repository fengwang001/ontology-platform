// 演示在线统计量累加器：数值稳定性、可合并性、边界错误与特殊值语义。
// 不读命令行参数、不联网；全部判定通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"

	"ontology"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s: %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s: %s\n", name, detail)
	}
}

func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Abs(want)
}

func main() {
	// 1. 1e9 偏移样本：真实样本方差 2.5，朴素公式在 float64 下垮掉。
	var acc ontology.Accumulator
	var sum, sumSq float64
	for i := 0; i < 5; i++ {
		x := 1e9 + float64(i)
		_ = acc.Add(x)
		sum += x
		sumSq += x * x
	}
	sv, _ := acc.SampleVariance()
	naive := (sumSq - sum*sum/5) / 4
	check("shifted-1e9", relErr(sv, 2.5) <= 1e-12,
		fmt.Sprintf("sample var=%.17g (true 2.5), naive formula=%.17g", sv, naive))

	// 2. 全等样本：方差精确为 0。
	var same ontology.Accumulator
	for i := 0; i < 1000; i++ {
		_ = same.Add(7.25)
	}
	pv, _ := same.PopulationVariance()
	sv2, _ := same.SampleVariance()
	check("identical-samples", pv == 0 && sv2 == 0,
		fmt.Sprintf("pop var=%v, sample var=%v (exact zero)", pv, sv2))

	// 3. 一万个样本随机分段、树状合并，与一次性喂入一致。
	rng := rand.New(rand.NewPCG(9, 18))
	const total = 10000
	samples := make([]float64, total)
	for i := range samples {
		samples[i] = 1e6 + rng.NormFloat64()*1e3
	}
	var oneShot ontology.Accumulator
	var segs []*ontology.Accumulator
	for i := 0; i < total; {
		size := rng.IntN(997) + 1
		if i+size > total {
			size = total - i
		}
		seg := &ontology.Accumulator{}
		for _, x := range samples[i : i+size] {
			_ = oneShot.Add(x)
			_ = seg.Add(x)
		}
		segs = append(segs, seg)
		i += size
	}
	for len(segs) > 1 {
		var next []*ontology.Accumulator
		for i := 0; i < len(segs); i += 2 {
			if i+1 < len(segs) {
				next = append(next, ontology.Merge(segs[i], segs[i+1]))
			} else {
				next = append(next, segs[i])
			}
		}
		segs = next
	}
	mGot, _ := segs[0].Mean()
	mWant, _ := oneShot.Mean()
	vGot, _ := segs[0].SampleVariance()
	vWant, _ := oneShot.SampleVariance()
	check("segmented-merge", relErr(mGot, mWant) <= 1e-12 && relErr(vGot, vWant) <= 1e-12,
		fmt.Sprintf("%d samples, mean rel err=%.2e, var rel err=%.2e",
			total, relErr(mGot, mWant), relErr(vGot, vWant)))

	// 4. Merge 不修改源累加器（逐位比较）。
	a := &ontology.Accumulator{}
	b := &ontology.Accumulator{}
	for _, x := range []float64{1, 2, 3, 4, 5} {
		_ = a.Add(x)
	}
	for _, x := range []float64{10, 20, 30} {
		_ = b.Add(x)
	}
	am0, _ := a.Mean()
	av0, _ := a.SampleVariance()
	bm0, _ := b.Mean()
	bv0, _ := b.SampleVariance()
	_ = ontology.Merge(a, b)
	am1, _ := a.Mean()
	av1, _ := a.SampleVariance()
	bm1, _ := b.Mean()
	bv1, _ := b.SampleVariance()
	sameBits := math.Float64bits(am0) == math.Float64bits(am1) &&
		math.Float64bits(av0) == math.Float64bits(av1) &&
		math.Float64bits(bm0) == math.Float64bits(bm1) &&
		math.Float64bits(bv0) == math.Float64bits(bv1) &&
		a.Count() == 5 && b.Count() == 3
	check("merge-non-mutating", sameBits, "sources bitwise identical after Merge")

	// 5. 零样本与单样本是两类可区分的错误。
	var empty ontology.Accumulator
	_, errEmpty := empty.Mean()
	one := &ontology.Accumulator{}
	_ = one.Add(42.5)
	om, _ := one.Mean()
	opv, _ := one.PopulationVariance()
	_, errOne := one.SampleVariance()
	check("boundary-errors",
		errors.Is(errEmpty, ontology.ErrNoSamples) &&
			!errors.Is(errEmpty, ontology.ErrInsufficientSamples) &&
			om == 42.5 && opv == 0 &&
			errors.Is(errOne, ontology.ErrInsufficientSamples) &&
			!errors.Is(errOne, ontology.ErrNoSamples),
		"empty->ErrNoSamples, single->mean=42.5 popvar=0 samplevar->ErrInsufficientSamples")

	// 6. NaN 被拒绝并计入跳过计数，不污染统计量。
	nanAcc := &ontology.Accumulator{}
	_ = nanAcc.Add(1)
	_ = nanAcc.Add(3)
	errNaN := nanAcc.Add(math.NaN())
	nm, _ := nanAcc.Mean()
	check("nan-rejected",
		errors.Is(errNaN, ontology.ErrNaNSample) && nanAcc.Skipped() == 1 &&
			nanAcc.Count() == 2 && nm == 2,
		fmt.Sprintf("skipped=%d, count=%d, mean=%v", nanAcc.Skipped(), nanAcc.Count(), nm))

	// 7. ±Inf 使统计量不可用，返回可判定错误而非 NaN。
	infAcc := &ontology.Accumulator{}
	_ = infAcc.Add(1)
	_ = infAcc.Add(math.Inf(1))
	iv, errInf := infAcc.Mean()
	check("inf-unavailable",
		errors.Is(errInf, ontology.ErrStatsUnavailable) && !math.IsNaN(iv),
		fmt.Sprintf("Mean err=%v", errInf))

	fmt.Printf("TOTAL: %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
