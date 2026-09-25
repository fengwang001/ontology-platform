// Command demo 对滚动哈希子串查询器做一组可运行判定；不读参数、不联网。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		fails++
	}
}

func naiveLCP(s []byte, i, j int) int {
	k := 0
	for i+k < len(s) && j+k < len(s) && s[i+k] == s[j+k] {
		k++
	}
	return k
}

func main() {
	// 1) 第三节演示参数 B=3、单模 M=7，s="abab" 的前缀哈希与底幂表。
	const B, M = 3, 7
	demo := []byte("abab")
	P := make([]int, len(demo)+1)
	pw := make([]int, len(demo)+1)
	pw[0] = 1
	for i := 1; i <= len(demo); i++ {
		P[i] = (P[i-1]*B + int(demo[i-1])) % M
		pw[i] = pw[i-1] * B % M
	}
	h24 := ((P[4]-P[2]*pw[2])%M + M) % M
	check("abab 前缀表 [0 6 4 4 5] 与底幂表 [1 3 2 6 4]",
		fmt.Sprint(P) == "[0 6 4 4 5]" && fmt.Sprint(pw) == "[1 3 2 6 4]")

	// 2) 三类错误须在构建前先验未构建态；顺序不可调换。
	_, eNot := api.Equal(0, 0, 0, 0)
	eEmpty := api.New(nil)
	_, eStill := api.LCP(0, 0)
	distinct := errors.Is(eNot, api.ErrNotBuilt) && errors.Is(eEmpty, api.ErrEmptyInput) &&
		errors.Is(eStill, api.ErrNotBuilt) && !errors.Is(api.ErrEmptyInput, api.ErrNotBuilt) &&
		!errors.Is(api.ErrOutOfRange, api.ErrNotBuilt) && !errors.Is(api.ErrOutOfRange, api.ErrEmptyInput)
	check("未构建/空串/越界三类哨兵可判定且互不相同", distinct)

	// 3) 构建真实双哈希查询器（此处之后才是构建态）。
	s := []byte("abababcabab")
	built := api.New(s) == nil
	eq0224, _ := api.Equal(0, 2, 2, 4)
	check("New 成功；Equal(0,2,2,4) 为真；s[2..4) 演示哈希为 4",
		built && eq0224 && h24 == 4)

	// 4) Equal 与 bytes.Equal 全枚举一致。
	naiveOK := true
	for l1 := 0; l1 <= len(s) && naiveOK; l1++ {
		for r1 := l1; r1 <= len(s); r1++ {
			for l2 := 0; l2 <= len(s); l2++ {
				for r2 := l2; r2 <= len(s); r2++ {
					got, err := api.Equal(l1, r1, l2, r2)
					if err != nil || got != bytes.Equal(s[l1:r1], s[l2:r2]) {
						naiveOK = false
					}
				}
			}
		}
	}
	check("Equal 与朴素 bytes.Equal 逐组一致", naiveOK)

	// 5) LCP 与逐字节比较全枚举一致。
	lcpOK := true
	for i := 0; i <= len(s); i++ {
		for j := 0; j <= len(s); j++ {
			got, err := api.LCP(i, j)
			if err != nil || got != naiveLCP(s, i, j) {
				lcpOK = false
			}
		}
	}
	check("LCP 与逐字节比较逐组一致", lcpOK)

	// 6) 越界拒绝；随后空串 New 被整体拒绝，旧态不变、仍可正常使用。
	_, eRange := api.Equal(0, 1, 9, 12)
	eEmpty2 := api.New(nil)
	stillEq, _ := api.Equal(0, 4, 7, 11) // [0,4)="abab" == [7,11)="abab"
	check("越界返回 ErrOutOfRange；拒绝不留痕、之后仍正常",
		errors.Is(eRange, api.ErrOutOfRange) && errors.Is(eEmpty2, api.ErrEmptyInput) && stillEq)

	// 7) m 次 Equal 重扫字符数恒 0、LCP 比较次数不超 log：由 SelfCheck 给成败。
	for m := 0; m < 500; m++ {
		if _, err := api.Equal(0, m%len(s), len(s)-m%len(s), len(s)); err != nil {
			break
		}
	}
	check("m 次 Equal 重扫为 0 且 LCP 比较次数 ≤ ceil(log2 n)+1", api.SelfCheck() == nil)

	// 8) N 个 goroutine 并发查询，结果逐项一致（无 sleep）。
	const N = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	rows := make([][3]bool, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			a, _ := api.Equal(0, 2, 2, 4)
			b, _ := api.Equal(0, 4, 7, 11)
			k1, _ := api.LCP(0, 4)
			rows[g] = [3]bool{a, b, k1 == 2}
		}(g)
	}
	close(start)
	wg.Wait()
	concOK := true
	for g := 1; g < N; g++ {
		if rows[g] != rows[0] {
			concOK = false
		}
	}
	check("并发 Equal/LCP 结果逐项一致", concOK)

	if fails > 0 {
		os.Exit(1)
	}
}
