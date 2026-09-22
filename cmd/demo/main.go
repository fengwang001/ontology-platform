// demo 逐项演练在线统计量累加器的关键性质，每项打印一行 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"

	"ontology"
)

var failures int

func check(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

// 朴素两遍公式：sumSq - sum^2/n，在 1e9 偏移样本上会坍缩成 0 或负数。
func naiveSampleVariance(xs []float64) float64 {
	var sum, sumSq float64
	for _, x := range xs {
		sum += x
		sumSq += x * x
	}
	n := float64(len(xs))
	return (sumSq - sum*sum/n) / (n - 1)
}

func main() {
	// 1. 1e9 偏移样本：真实样本方差 2.5，朴素公式在 float64 下坍缩。
	offset := []float64{1e9, 1e9 + 1, 1e9 + 2, 1e9 + 3, 1e9 + 4}
	a := ontology.New()
	for _, x := range offset {
		_ = a.Add(x)
	}
	sv, err := a.SampleVariance()
	naive := naiveSampleVariance(offset)
	check(err == nil && math.Abs(sv-2.5)/2.5 < 1e-12,
		"1e9 偏移样本: 样本方差=%.17g 真实值=2.5 (朴素公式=%.6g, 已失真)", sv, naive)

	// 2. 全等样本：方差必须是精确的 0。
	b := ontology.New()
	for i := 0; i < 1000; i++ {
		_ = b.Add(3.14)
	}
	pv, err := b.Variance()
	check(err == nil && pv == 0, "全等样本: 总体方差=%g (精确的 0, 非负数)", pv)

	// 3. 一万个样本随机切段、两两合并，与一次性喂入一致。
	rng := rand.New(rand.NewPCG(2026, 9))
	const total = 10000
	xs := make([]float64, total)
	oneShot := ontology.New()
	for i := range xs {
		xs[i] = rng.NormFloat64()*100 + 5
		_ = oneShot.Add(xs[i])
	}
	var segments []*ontology.Accumulator
	for i := 0; i < total; {
		size := 1 + rng.IntN(97)
		if i+size > total {
			size = total - i
		}
		seg := ontology.New()
		for _, x := range xs[i : i+size] {
			_ = seg.Add(x)
		}
		segments = append(segments, seg)
		i += size
	}
	merged := segments[0]
	for _, seg := range segments[1:] {
		merged = ontology.Merge(merged, seg)
	}
	mMean, _ := merged.Mean()
	oMean, _ := oneShot.Mean()
	mVar, _ := merged.SampleVariance()
	oVar, _ := oneShot.SampleVariance()
	check(math.Abs(mMean-oMean)/math.Abs(oMean) < 1e-12 && math.Abs(mVar-oVar)/math.Abs(oVar) < 1e-12,
		"随机分段合并(%d 段): 均值=%.15g 方差=%.15g, 与一次性喂入一致", len(segments), mMean, mVar)

	// 4. Merge 不修改源累加器。
	sa := ontology.New()
	sb := ontology.New()
	for _, x := range []float64{1, 2, 3, 4} {
		_ = sa.Add(x)
	}
	for _, x := range []float64{10, 20, 30} {
		_ = sb.Add(x)
	}
	aMeanBefore, _ := sa.Mean()
	bMeanBefore, _ := sb.Mean()
	_ = ontology.Merge(sa, sb)
	aMeanAfter, _ := sa.Mean()
	bMeanAfter, _ := sb.Mean()
	check(math.Float64bits(aMeanBefore) == math.Float64bits(aMeanAfter) &&
		math.Float64bits(bMeanBefore) == math.Float64bits(bMeanAfter),
		"Merge 前后源累加器读数逐位不变 (a.mean=%g b.mean=%g)", aMeanAfter, bMeanAfter)

	// 5. 零样本与单样本是两类可区分的错误。
	empty := ontology.New()
	_, errEmpty := empty.Mean()
	one := ontology.New()
	_ = one.Add(42.0)
	oneMean, _ := one.Mean()
	_, errOne := one.SampleVariance()
	check(errors.Is(errEmpty, ontology.ErrNoSamples) &&
		errors.Is(errOne, ontology.ErrZeroDegreesOfFreedom) &&
		!errors.Is(errOne, ontology.ErrNoSamples) && oneMean == 42,
		"零样本错误=%v; 单样本均值=%g, 样本方差错误=%v", errEmpty, oneMean, errOne)

	// 6. NaN 被拒绝并计入跳过计数，不污染统计量。
	n := ontology.New()
	_ = n.Add(1)
	_ = n.Add(math.NaN())
	_ = n.Add(math.NaN())
	_ = n.Add(3)
	nMean, _ := n.Mean()
	check(n.Skipped() == 2 && n.Count() == 2 && nMean == 2,
		"NaN 拒绝: 跳过=%d 有效计数=%d 均值=%g (未污染)", n.Skipped(), n.Count(), nMean)

	// 7. 无穷样本使统计量不可用，返回可判定错误而非 NaN。
	inf := ontology.New()
	_ = inf.Add(1)
	_ = inf.Add(math.Inf(1))
	v, errInf := inf.Mean()
	check(errors.Is(errInf, ontology.ErrUnusable) && !math.IsNaN(v),
		"无穷样本: Mean 返回错误=%v (统计量已不可用, 不返回 NaN)", errInf)

	fmt.Printf("总计: %d 项检查, %d 项失败\n", 7, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
