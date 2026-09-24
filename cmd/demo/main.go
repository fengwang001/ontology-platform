// Command demo 校验有界乱序重排缓冲，逐条打印 OK/FAIL，全部通过时退出码 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"ontology/order"
	"os"
	"slices"
	"strings"
	"sync"
)

var failed int

func check(name string, ok bool) {
	if !ok {
		failed++
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

// ids 把一串输出拼成 "b,a,c" 便于比较。
func ids(outs []api.Out) (s string) {
	for _, o := range outs {
		s += o.ID + ","
	}
	return strings.TrimSuffix(s, ",")
}

var demoIDs = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
var demoTS = []int64{5, 3, 9, 5, 6, 9, 4, 12, 12, 16}

// checkRandom 随机乱序下与朴素参照逐条一致，且主输出严格有序。
func checkRandom() (refOK, strictOK bool) {
	refOK, strictOK = true, true
	const delay = int64(9)
	for _, n := range []int{50, 200} {
		rng := rand.New(rand.NewSource(int64(n)))
		r, _ := api.New(delay, n+2)
		var maxTS int64
		var refMain, refSide []api.Out
		for i := 0; i < n; i++ {
			o := api.Out{ID: fmt.Sprintf("e%03d", i), TS: int64(rng.Intn(n/2 + 2)), Seq: int64(i)}
			if i > 0 && o.TS <= maxTS-delay {
				refSide = append(refSide, o)
			} else {
				refMain = append(refMain, o)
			}
			maxTS = max(maxTS, o.TS)
			r.Push(o.ID, o.TS) // ID 唯一、缓冲 n+2 够大，不会失败
		}
		r.Flush()
		slices.SortStableFunc(refMain, func(a, b api.Out) int { return int(a.TS - b.TS) })
		main := r.Main()
		refOK = refOK && slices.Equal(main, refMain) && slices.Equal(r.Side(), refSide)
		for i := 1; i < len(main); i++ {
			a, b := main[i-1], main[i]
			strictOK = strictOK && order.Less(order.Key{TS: a.TS, Seq: a.Seq}, order.Key{TS: b.TS, Seq: b.Seq})
		}
	}
	return refOK, strictOK
}

// checkErrors 四类故障注入各有可判定哨兵错误；被拒后状态不变、不耗 Seq、仍可用。
func checkErrors() (kindsOK, noTraceOK bool) {
	r, _ := api.New(10, 1)
	r.Push("x", 100) // wm=90，x 滞留缓冲占满名额
	m0, s0 := ids(r.Main()), ids(r.Side())
	_, e1 := api.New(-1, 5)
	_, e2 := api.New(3, 0)
	_, _, e3 := r.Push("", 1)
	_, _, e4 := r.Push("x", 1)
	_, _, e5 := r.Push("y", 101)
	kindsOK = errors.Is(e1, api.ErrInvalidParam) && errors.Is(e2, api.ErrInvalidParam) &&
		errors.Is(e3, api.ErrEmptyID) && errors.Is(e4, api.ErrDuplicateID) &&
		errors.Is(e5, api.ErrBufferFull)
	noTraceOK = ids(r.Main()) == m0 && ids(r.Side()) == s0
	_, side, err := r.Push("y", 1) // 迟到不占缓冲；Seq 应紧接 x 的 0
	noTraceOK = noTraceOK && err == nil && len(side) == 1 && side[0].Seq == 1
	return kindsOK, noTraceOK
}

func checkBigM() bool {
	for _, m := range []int{100, 1000, 10000} {
		r, _ := api.New(int64(m), m+1)
		for i := 0; i < m; i++ { // TS=1..m，wm 恒 <=0，全部滞留
			r.Push(fmt.Sprintf("h%d", i), int64(i+1))
		}
		main, _, err := r.Push("t", int64(m+3)) // wm 推到 3，恰好释放 h0..h2
		if err != nil || len(main) != 3 || main[2].ID != "h2" {
			return false
		}
	}
	return true
}

// checkConcurrent 多 goroutine 并发 Push 不同 ID，结束后每个事件恰好出现一次。
func checkConcurrent() bool {
	const G, K = 8, 25
	r, _ := api.New(7, G*K)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < K; k++ {
				r.Push(fmt.Sprintf("g%d-%d", g, k), int64((g*7+k*13)%(G*K/2)))
			}
		}(g)
	}
	wg.Wait()
	r.Flush()
	seen := map[string]bool{}
	for _, o := range slices.Concat(r.Main(), r.Side()) {
		if seen[o.ID] {
			return false
		}
		seen[o.ID] = true
	}
	return len(seen) == G*K
}

func main() {
	r, _ := api.New(3, 10)
	wantM := []string{"", "", "b,a", "", "", "", "", "c,f", "", "h,i"}
	wantS := []string{"", "", "", "d", "e", "", "g", "", "", ""}
	ok := true
	for i := range demoIDs {
		m, s, err := r.Push(demoIDs[i], demoTS[i])
		ok = ok && err == nil && ids(m) == wantM[i] && ids(s) == wantS[i]
	}
	check("十步每步主/旁路输出(第5步e迟到,第8步c→f,第10步h→i)", ok)
	check("Flush后完整主输出与旁路", ids(r.Flush()) == "j" &&
		ids(r.Main()) == "b,a,c,f,h,i,j" && ids(r.Side()) == "d,e,g")
	check("SelfCheck四条不变量", r.SelfCheck() == nil)
	refOK, strictOK := checkRandom()
	check("随机乱序与朴素参照一致", refOK)
	check("主输出严格有序", strictOK)
	kindsOK, noTraceOK := checkErrors()
	check("四类可判定错误", kindsOK)
	check("被拒后状态不变", noTraceOK)
	check("大m缓冲恰好释放k个(探针上界见rbuf测试)", checkBigM())
	check("并发Push后恰好一次", checkConcurrent())
	if failed > 0 {
		os.Exit(1)
	}
}
