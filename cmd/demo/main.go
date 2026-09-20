// demo 逐条演练分位数查询器的边界语义与插值口径。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %-22s %s\n", verdict, name, detail)
}

func main() {
	// 1. 分位点落在样本之间：两种口径结果不同。
	q := ontology.New()
	for _, v := range []float64{1, 2, 3, 4} {
		q.Add(v)
	}
	nr, _ := q.Quantile(0.5, ontology.NearestRank)
	li, _ := q.Quantile(0.5, ontology.Linear)
	check("口径互不相同", nr != li, fmt.Sprintf("p=0.5 NR=%v R7=%v", nr, li))

	// 2. 分位点落在样本点上：两种口径一致。
	q2 := ontology.New()
	for _, v := range []float64{10, 20, 30, 40, 50} {
		q2.Add(v)
	}
	nr2, _ := q2.Quantile(0.5, ontology.NearestRank)
	li2, _ := q2.Quantile(0.5, ontology.Linear)
	check("样本点处一致", nr2 == li2 && nr2 == 30,
		fmt.Sprintf("p=0.5 NR=%v R7=%v", nr2, li2))

	// 3. p=0 与 p=1 返回真实最小/最大值。
	lo, _ := q2.Quantile(0, ontology.Linear)
	hi, _ := q2.Quantile(1, ontology.Linear)
	check("p=0/p=1 边界", lo == 10 && hi == 50,
		fmt.Sprintf("min=%v max=%v", lo, hi))

	// 4. p 非法与空数据集是两类可区分的错误。
	_, errP := q2.Quantile(math.NaN(), ontology.Linear)
	_, errE := ontology.New().Quantile(0.5, ontology.Linear)
	check("两类错误可区分",
		errors.Is(errP, ontology.ErrInvalidP) &&
			errors.Is(errE, ontology.ErrEmpty) &&
			!errors.Is(errP, ontology.ErrEmpty),
		fmt.Sprintf("invalidP=%v empty=%v", errP, errE))

	// 5. 权重 3 与重复 3 次逐位相同。
	qw, qe := ontology.New(), ontology.New()
	qw.AddWeighted(1.5, 3)
	qw.AddWeighted(2.5, 1)
	qe.Add(1.5)
	qe.Add(1.5)
	qe.Add(1.5)
	qe.Add(2.5)
	a, _ := qw.Quantile(0.7, ontology.Linear)
	b, _ := qe.Quantile(0.7, ontology.Linear)
	check("权重等价于展开", math.Float64bits(a) == math.Float64bits(b),
		fmt.Sprintf("weighted=%v expanded=%v", a, b))

	// 6. 百万总权重下唯一值个数仍然很小。
	qm := ontology.New()
	for i := 0; i < 40; i++ {
		qm.AddWeighted(float64(i), 30000)
	}
	check("百万权重少量唯一值",
		qm.TotalWeight() == 1_200_000 && qm.UniqueCount() == 40,
		fmt.Sprintf("total=%d unique=%d", qm.TotalWeight(), qm.UniqueCount()))

	// 7. ±0.0 合并。
	qz := ontology.New()
	qz.Add(math.Copysign(0, -1))
	qz.Add(0.0)
	z, _ := qz.Quantile(0.5, ontology.Linear)
	check("±0.0 合并", qz.UniqueCount() == 1 &&
		math.Float64bits(z) == math.Float64bits(0.0),
		fmt.Sprintf("unique=%d q=%v", qz.UniqueCount(), z))

	// 8. NaN 被跳过并计数。
	qn := ontology.New()
	qn.Add(1)
	qn.Add(math.NaN())
	qn.Add(math.NaN())
	check("NaN 跳过计数", qn.SkippedNaN() == 2 && qn.UniqueCount() == 1,
		fmt.Sprintf("skipped=%d", qn.SkippedNaN()))

	// 9. 无穷参与的分位点。
	qi := ontology.New()
	qi.Add(1)
	qi.Add(math.Inf(1))
	inf, _ := qi.Quantile(0.5, ontology.Linear)
	check("无穷分位点", math.IsInf(inf, 1), fmt.Sprintf("R7(0.5)=%v", inf))

	// 10. 打乱加入顺序结果逐位一致。
	src := rand.New(rand.NewSource(1))
	samples := make([]float64, 200)
	for i := range samples {
		samples[i] = math.Round(src.NormFloat64()*20) / 2
	}
	qa, qb := ontology.New(), ontology.New()
	for _, v := range samples {
		qa.Add(v)
	}
	for _, idx := range rand.New(rand.NewSource(2)).Perm(len(samples)) {
		qb.Add(samples[idx])
	}
	same := true
	for p := 0.0; p <= 1.0; p += 0.01 {
		for _, m := range []ontology.Method{ontology.NearestRank, ontology.Linear} {
			x, _ := qa.Quantile(p, m)
			y, _ := qb.Quantile(p, m)
			if math.Float64bits(x) != math.Float64bits(y) {
				same = false
			}
		}
	}
	check("乱序加入一致", same, "200 样本 x 202 查询逐位相同")

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
