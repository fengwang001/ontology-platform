// demo 逐条演练加权水塘抽样器的各项保证，每步打印一行 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func feed(s *ontology.Sampler, n int) {
	for i := 0; i < n; i++ {
		_ = s.Add(i, float64(i%7+1))
	}
}

func main() {
	// 1. 同种子两次抽样逐元素一致，且随机数消耗相同。
	s1, _ := ontology.NewSampler(10, 42)
	s2, _ := ontology.NewSampler(10, 42)
	feed(s1, 1000)
	feed(s2, 1000)
	same := reflect.DeepEqual(s1.Sample(), s2.Sample()) &&
		s1.RandConsumed() == s2.RandConsumed()
	check("same-seed-reproducible", same,
		fmt.Sprintf("rand=%d", s1.RandConsumed()))

	// 2. 不同种子产出不同结果。
	sa, _ := ontology.NewSampler(5, 1)
	sb, _ := ontology.NewSampler(5, 2)
	feed(sa, 200)
	feed(sb, 200)
	check("different-seeds-differ", !reflect.DeepEqual(sa.Sample(), sb.Sample()), "")

	// 3. 权重 1:2:3 下 2000 轮入选频率对比期望比例。
	const rounds = 2000
	counts := [3]int{}
	for r := 0; r < rounds; r++ {
		s, _ := ontology.NewSampler(1, uint64(0xC0FFEE+r))
		for i, w := range []float64{1, 2, 3} {
			_ = s.Add(i, w)
		}
		counts[s.Sample()[0].(int)]++
	}
	f0, f1, f2 := float64(counts[0])/rounds, float64(counts[1])/rounds, float64(counts[2])/rounds
	ok := abs(f0-1.0/6) < 0.05 && abs(f1-2.0/6) < 0.05 && abs(f2-3.0/6) < 0.05
	check("weight-ratio-frequencies", ok,
		fmt.Sprintf("got=%.3f/%.3f/%.3f want=0.167/0.333/0.500", f0, f1, f2))

	// 4. k<=0 返回可判定错误。
	_, err := ontology.NewSampler(0, 1)
	check("non-positive-k-error", errors.Is(err, ontology.ErrInvalidCapacity), fmt.Sprint(err))

	// 5. 流长度小于 k 时返回全部（到达顺序）。
	s5, _ := ontology.NewSampler(10, 3)
	for i := 0; i < 4; i++ {
		_ = s5.Add(i, 1)
	}
	check("short-stream-returns-all", reflect.DeepEqual(s5.Sample(), []any{0, 1, 2, 3}),
		fmt.Sprint(s5.Sample()))

	// 6. 非法权重被拒绝且计数正确。
	s6, _ := ontology.NewSampler(4, 5)
	bad := 0
	for _, w := range []float64{0, -1, 1.5} {
		if errors.Is(s6.Add("bad", w), ontology.ErrInvalidWeight) {
			bad++
		}
	}
	_ = s6.Add("good", 2)
	check("invalid-weight-rejected", bad == 3 && s6.Rejected() == 3 &&
		s6.Total() == 1 && s6.Size() == 1,
		fmt.Sprintf("rejected=%d total=%d", s6.Rejected(), s6.Total()))

	// 7. 百万元素流上水塘大小始终不超过 k。
	s7, _ := ontology.NewSampler(100, 2024)
	bounded := true
	for i := 0; i < 1_000_000; i++ {
		_ = s7.Add(i, float64(i%5+1))
		if i%4096 == 0 && s7.Size() > 100 {
			bounded = false
		}
	}
	check("million-stream-bounded", bounded && s7.Size() == 100,
		fmt.Sprintf("size=%d k=100", s7.Size()))

	// 8. 无新增元素时两次 Sample 结果一致。
	check("sample-idempotent", reflect.DeepEqual(s7.Sample(), s7.Sample()), "")

	fmt.Printf("SUMMARY %d checks, %d failed\n", 8, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
