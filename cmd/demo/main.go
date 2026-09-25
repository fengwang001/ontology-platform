package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/idx"
	"ontology/norm"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func k(s string) string { k, _ := norm.Key(s); return k }

func main() {
	// 第三节八个输入（码点序列）与其 NFC 键
	in := []string{
		"\u00e9", "e\u0301", "e\u0301\u0323", "e\u0323\u0301",
		"\u1100\u1161", "\uac00", "\u212b", "\u00c5",
	}
	want := []string{
		"\u00e9", "\u00e9", "\u1eb9\u0301", "\u1eb9\u0301",
		"\uac00", "\uac00", "\u00c5", "\u00c5",
	}
	ok := true
	for i := range in {
		ok = k(in[i]) == want[i] && ok
	}
	check("eight-inputs keys=4", ok) // 最终不同键数=4（é/ẹ+acute/가/Å）
	// 甲：规范排序。正确键 1EB9 0301；不排序的错键 00E9 0323
	wrongJia := "\u00e9\u0323"
	check("jia right=1EB9+0301 wrong=00E9+0323",
		k(in[2]) == want[2] && k(in[3]) == want[2] && wrongJia != want[2])
	// 乙：韩文算法分解。NFD(AC00) 必须是 2 码点 1100 1161，错值是 1 码点
	nfd6, _ := norm.NFD(in[5])
	check("yi right=NFD(AC00)=1100+1161 wrong=1cp",
		nfd6 == "\u1100\u1161" && nfd6 != in[5] && k(in[4]) == want[4])
	// 丙：组合排除。A+ring 只组合成 00C5；错误还原 212B 与正确键不同
	wrongBing := "\u212b"
	check("bing right=00C5 wrong=212B",
		k(in[6]) == want[6] && k(in[7]) == want[6] && wrongBing != want[6])
	// 幂等且 NFC 与原文规范等价
	id := true
	for _, s := range in {
		n1, _ := norm.NFD(s)
		c1, _ := norm.NFC(s)
		n2, _ := norm.NFD(n1)
		c2, _ := norm.NFC(c1)
		d1, _ := norm.NFD(c1)
		id = id && n1 == n2 && c1 == c2 && d1 == n1
	}
	check("idempotent-nfd-nfc", id)
	// 等价键一致：Get 返回等价串集合，Distinct=4
	a, _ := api.New(100)
	for _, s := range in {
		a.Put(s)
	}
	g2, _ := a.Get(in[1])
	g3, _ := a.Get(in[3])
	check("equiv-get-set distinct=4", a.Distinct() == 4 &&
		len(g2) == 2 && g2[0] == in[0] && g2[1] == in[1] &&
		len(g3) == 2 && g3[0] == in[2] && g3[1] == in[3])
	// 四类可判定错误互不相同；被拒后状态不变
	_, e1 := api.New(0)
	e2 := a.Put("\xff")
	e3 := a.Put("0")
	full, _ := api.New(1)
	full.Put("x")
	e4 := full.Put("y")
	dist := map[error]bool{e1: true, e2: true, e3: true, e4: true}
	g2b, _ := a.Get(in[1])
	check("four-errors state-intact", len(dist) == 4 &&
		errors.Is(e1, idx.ErrBadMaxKeys) && errors.Is(e2, norm.ErrInvalidUTF8) &&
		errors.Is(e3, norm.ErrUnsupported) && errors.Is(e4, idx.ErrTooManyKeys) &&
		a.Distinct() == 4 && len(g2b) == 2)
	// 大 m 下 Get 结果不随 m 增长（检查个数的证明见 idx 包内测试）
	lm := true
	for _, m := range []int{100, 1000, 10000} {
		big, _ := api.New(m)
		for i := 0; i < m; i++ {
			big.Put(string(rune(0xAC00 + i)))
		}
		g, _ := big.Get(string(rune(0xAC00 + m/2)))
		lm = lm && big.Distinct() == m && len(g) == 1
	}
	check("large-m get O(1)", lm)
	// 并发 Put/Get 结果一致，Distinct 单调不减（不用 sleep）
	const N = 256
	c, _ := api.New(N)
	done := make(chan struct{})
	var samples []int
	var swg, wg sync.WaitGroup
	swg.Add(1)
	go func() {
		defer swg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			samples = append(samples, c.Distinct())
		}
	}()
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Put(string(rune(0xAC00 + i))) }()
	}
	wg.Wait()
	close(done)
	swg.Wait()
	got := make([][]string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, _ := c.Get(string(rune(0xAC00 + i)))
			got[i] = g
		}()
	}
	wg.Wait()
	cc := c.Distinct() == N
	for i := 0; i < N; i++ {
		cc = cc && len(got[i]) == 1 && got[i][0] == string(rune(0xAC00+i))
	}
	for i := 1; i < len(samples); i++ {
		cc = cc && samples[i] >= samples[i-1]
	}
	check("concurrent-put-get", cc)

	check("selfcheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
