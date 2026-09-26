// 加权中位数演示：逐条打印 OK/FAIL，全部通过退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"ontology/api"
	"ontology/wmid"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	s := "OK"
	if !ok {
		s = "FAIL"
	}
	fmt.Printf("%s %s\n", s, name)
}

func naive(pairs [][2]int64) (int64, int64) {
	sorted := append([][2]int64(nil), pairs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	var W, p int64
	for _, e := range sorted {
		W += e[1]
	}
	for _, e := range sorted {
		p += e[1]
		if 2*p >= W {
			return e[0], W
		}
	}
	return 0, 0
}

func main() {
	// 1. 第三节四元素：前缀权重与 Median。
	seq := [][2]int64{{30, 3}, {10, 3}, {40, 3}, {20, 3}}
	st := api.New()
	for _, p := range seq {
		_ = st.Insert(p[0], p[1])
	}
	m, err := st.Median()
	check(fmt.Sprintf("四元素前缀P=[3,6,9,12] Median=%d(期望20)", m), err == nil && m == 20)

	// 2+3+4. 两侧权重约束、最小性、与朴素参照一致（多组序列）。
	okInv := true
	seqs := [][][2]int64{seq, {{5, 1}, {1, 10}, {9, 1}}, {{7, 2}, {3, 5}, {11, 4}, {1, 1}, {9, 6}}}
	for _, sq := range seqs {
		s2 := api.New()
		for _, p := range sq {
			_ = s2.Insert(p[0], p[1])
		}
		mm, _ := s2.Median()
		nm, W := naive(sq)
		var below, above int64
		for _, p := range sq {
			if p[0] < mm {
				below += p[1]
			} else if p[0] > mm {
				above += p[1]
			}
		}
		okInv = okInv && mm == nm && 2*below <= W && 2*above <= W && 2*below < W
	}
	check("两侧权重约束+最小性+朴素参照一致", okInv)

	// 5. 三类可判定错误互不相同。
	e1 := api.New().Insert(1, 0)
	s3 := api.New()
	_ = s3.Insert(1, 1)
	e2 := s3.Insert(1, 1)
	_, e3 := api.New().Median()
	check("三类错误可判定且互异",
		errors.Is(e1, api.ErrNonPositiveWeight) && errors.Is(e2, api.ErrDuplicateValue) &&
			errors.Is(e3, api.ErrEmpty) && e1 != e2 && e2 != e3 && e1 != e3)

	// 6. 被拒后状态不变。
	wBefore, mBefore := s3.Total(), func() int64 { v, _ := s3.Median(); return v }()
	_ = s3.Insert(1, 9)
	_ = s3.Insert(2, -1)
	mAfter, _ := s3.Median()
	check("被拒操作后状态不变", s3.Total() == wBefore && mAfter == mBefore)

	// 7. 大 m 下遍历节点数不随 m 线性增长（遍历路径长 <= 树高）。
	okDepth := true
	for _, mn := range []int{100, 1000, 10000} {
		t := wmid.New()
		for i := 0; i < mn; i++ {
			_ = t.Insert(int64(i*2+1), int64(i%7+1))
		}
		bound := 2
		for x := mn; x > 1; x = (x + 1) / 2 {
			bound += 2
		}
		okDepth = okDepth && t.Depth() <= bound
	}
	check("大m下Median遍历节点数不随m增长", okDepth)

	// 8. 并发查询结果一致。
	sc := api.New()
	for i := 0; i < 500; i++ {
		_ = sc.Insert(int64(i*3+1), int64(i%5+1))
	}
	want, _ := sc.Median()
	var wg sync.WaitGroup
	res := make(chan int64, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _ := sc.Median()
			res <- v
		}()
	}
	wg.Wait()
	close(res)
	okConc := true
	for v := range res {
		okConc = okConc && v == want
	}
	check("并发查询结果一致", okConc)

	// 9. 自检。
	check("SelfCheck", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
