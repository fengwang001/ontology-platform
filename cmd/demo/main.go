// Command demo 逐条核验最少连接负载均衡的各项判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/lc"
	"ontology/svc"
)

var failed bool

func check(name string, ok bool) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", tag, name)
}

func main() {
	// lc 包：堆顶定位与朴素扫描一致（小规模手算序列）。
	c := lc.New(3)
	c.Incr(0)
	c.Incr(2)
	p1 := c.Pick() // (1,0,1) -> 1
	c.Incr(1)
	p2 := c.Pick() // (1,1,1) 并列 -> 0
	c.Decr(0)
	p3 := c.Pick() // (0,1,1) -> 0
	check("lc: heap pick matches naive scan", p1 == 1 && p2 == 0 && p3 == 0)

	// svc 包：下标越界与下溢是互不相同的可判定错误，且失败不留痕。
	s := svc.New(3)
	_ = s.Acquire(1)
	errIdx := s.Acquire(3)
	errUnd := s.Release(0)
	cnt, _ := s.Count(1)
	check("svc: ErrIndex/ErrUnderflow distinct, state untouched",
		errors.Is(errIdx, svc.ErrIndex) && errors.Is(errUnd, svc.ErrUnderflow) &&
			errIdx != errUnd && cnt == 1 && s.Pick() == 0)

	// api 包：题目八步序列，逐步核对连接数与结果（含第 8 步下溢报错）。
	lb, errCfg := api.New(3)
	steps := []struct {
		op    string
		i     int
		want  [3]int // 操作后 (c0,c1,c2)
		pick  int    // 仅 Pick 有效
		isErr bool
	}{
		{"acq", 0, [3]int{1, 0, 0}, 0, false},
		{"acq", 2, [3]int{1, 0, 1}, 0, false},
		{"pick", 0, [3]int{1, 0, 1}, 1, false},
		{"acq", 1, [3]int{1, 1, 1}, 0, false},
		{"pick", 0, [3]int{1, 1, 1}, 0, false},
		{"rel", 0, [3]int{0, 1, 1}, 0, false},
		{"pick", 0, [3]int{0, 1, 1}, 0, false},
		{"rel", 0, [3]int{0, 1, 1}, 0, true},
	}
	ok8 := errCfg == nil
	for _, st := range steps {
		var err error
		switch st.op {
		case "acq":
			err = lb.Acquire(st.i)
		case "rel":
			err = lb.Release(st.i)
		case "pick":
			ok8 = ok8 && lb.Pick() == st.pick
		}
		ok8 = ok8 && (err != nil) == st.isErr
		for j := 0; j < 3; j++ {
			cj, _ := lb.Count(j)
			ok8 = ok8 && cj == st.want[j]
		}
	}
	check("api: 8-step sequence counts & results match NOTES.md", ok8)

	// 三类可判定错误互不相同；New 配置非法。
	_, errBad := api.New(0)
	check("api: ErrConfig/ErrIndex/ErrUnderflow distinct",
		errors.Is(errBad, api.ErrConfig) && !errors.Is(errBad, api.ErrIndex) &&
			!errors.Is(api.ErrIndex, api.ErrUnderflow))

	// 自检：四条不变量（朴素一致/非负/守恒/失败不留痕）。
	check("api: SelfCheck invariants", lb.SelfCheck() == nil)

	// 大 m：m=10000，前 1000 台连接数互异，Pick 结果正确
	//（检查个数不随 m 增长由 lc 白盒测试钉住，不经导出接口读取）。
	big, _ := api.New(10000)
	for i := 1; i < 1000; i++ {
		for j := 0; j < i; j++ {
			_ = big.Acquire(i)
		}
	}
	check("api: large-m pick correct (bounded checks proven in lc test)", big.Pick() == 0)

	// 并发：N 个 goroutine 各 Acquire(0) 一次，最终 Count(0)==N。
	const N = 500
	clb, _ := api.New(4)
	var wg sync.WaitGroup
	for k := 0; k < N; k++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = clb.Acquire(0) }()
	}
	wg.Wait()
	got, _ := clb.Count(0)
	check("api: concurrent Acquire count exact", got == N)

	if failed {
		os.Exit(1)
	}
}
