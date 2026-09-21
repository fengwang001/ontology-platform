// demo 实际演练多路有序流集合运算器的各项能力，
// 每步打印一行以 OK 或 FAIL 开头的判定，最后打印总计。
package main

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"ontology"
)

var passed, total int

func check(ok bool, format string, args ...any) {
	total++
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func main() {
	s1 := []float64{1, 2, 2, 3, 3, 3}
	s2 := []float64{2, 2, 3, 4}
	s3 := []float64{3, 3, 5}

	setU, _, err1 := ontology.Union(ontology.Set, s1, s2, s3)
	multiU, _, err2 := ontology.Union(ontology.Multiset, s1, s2, s3)
	check(err1 == nil && err2 == nil &&
		slices.Equal(setU, []float64{1, 2, 3, 4, 5}) &&
		slices.Equal(multiU, []float64{1, 2, 2, 3, 3, 3, 4, 5}),
		"并集 集合=%v 多重集=%v", setU, multiU)

	setI, _, err1 := ontology.Intersect(ontology.Set, s1, s2, s3)
	multiI, _, err2 := ontology.Intersect(ontology.Multiset, s1, s2, s3)
	check(err1 == nil && err2 == nil &&
		slices.Equal(setI, []float64{3}) &&
		slices.Equal(multiI, []float64{3}),
		"交集 集合=%v 多重集=%v（本组最小次数为 1）", setI, multiI)

	m1, m2, m3 := []float64{7, 7, 7}, []float64{7}, []float64{7, 7}
	maxU, _, _ := ontology.Union(ontology.Multiset, m1, m2, m3)
	minI, _, _ := ontology.Intersect(ontology.Multiset, m1, m2, m3)
	check(slices.Equal(maxU, []float64{7, 7, 7}) && slices.Equal(minI, []float64{7}),
		"多重集 并集取最大次数=%v 交集取最小次数=%v", maxU, minI)

	diff, _, err := ontology.Difference(ontology.Multiset,
		[]float64{5, 5, 5, 5}, []float64{5}, []float64{5, 5})
	neg, _, _ := ontology.Difference(ontology.Multiset,
		[]float64{1, 2}, []float64{1, 1, 1, 2, 2, 2})
	check(err == nil && slices.Equal(diff, []float64{5}) && len(neg) == 0,
		"差集 4-1-2=%v 1-3 截断为零=%v", diff, neg)

	nz := math.Copysign(0, -1)
	zero, _, err := ontology.Intersect(ontology.Set, []float64{nz}, []float64{0.0})
	check(err == nil && len(zero) == 1, "+0.0 与 -0.0 同值，交集大小=%d", len(zero))

	_, _, nanErr := ontology.Union(ontology.Set,
		[]float64{1, 2}, []float64{3, math.NaN()})
	var ne ontology.NaNError
	check(errors.As(nanErr, &ne) && ne.Stream == 1 && ne.Index == 1,
		"NaN 错误定位: %v", nanErr)

	_, _, ordErr := ontology.Union(ontology.Set,
		[]float64{1, 2, 3}, []float64{0, 9, 4})
	var oe ontology.OrderError
	check(errors.As(ordErr, &oe) && oe.Stream == 1 && oe.Index == 2,
		"乱序错误定位: %v", ordErr)

	_, _, noErr := ontology.Intersect(ontology.Set)
	check(errors.Is(noErr, ontology.ErrNoStreams),
		"零条流的交集: %v", noErr)

	const k = 4
	const per = 250_000
	streams := make([][]float64, k)
	for i := range streams {
		streams[i] = make([]float64, per)
		for j := range streams[i] {
			streams[i][j] = float64(i + j*k)
		}
	}
	big, stats, err := ontology.Union(ontology.Set, streams...)
	bound := int64(3 * k * per * 2) // 3*N*ceil(log2(k))，k=4 时 levels=2
	check(err == nil && len(big) == k*per &&
		stats.MaxHeapSize <= k && stats.Comparisons <= bound,
		"百万级 N=%d 堆峰值=%d(<=4) 比较=%d(<=%d)",
		k*per, stats.MaxHeapSize, stats.Comparisons, bound)

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
}
