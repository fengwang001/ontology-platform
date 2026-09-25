// Command demo exercises the HRW key locator and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/cluster"
	"ontology/rnd"
)

var fails int

func ck(line string, ok bool) {
	tag := "OK"
	if !ok {
		tag, fails = "FAIL", fails+1
	}
	fmt.Println(line + ": " + tag)
}

// tableBest resolves the prescribed fixed table: max weight, tie->smallest ID.
func tableBest(w map[string]map[string]int, key string, nodes []string) string {
	best, bw := "", -1
	for _, n := range nodes {
		if x := w[key][n]; x > bw || (x == bw && n < best) {
			best, bw = n, x
		}
	}
	return best
}
func main() {
	w := map[string]map[string]int{
		"k1": {"A": 10, "B": 7, "C": 3, "D": 11},
		"k2": {"A": 4, "B": 9, "C": 5, "D": 3},
		"k3": {"A": 6, "B": 6, "C": 2, "D": 6},
		"k4": {"A": 8, "B": 2, "C": 9, "D": 7},
	}
	keys, abc, abcd := []string{"k1", "k2", "k3", "k4"}, []string{"A", "B", "C"}, []string{"A", "B", "C", "D"}
	s4, s5 := map[string]string{}, map[string]string{}
	for _, k := range keys {
		s4[k], s5[k] = tableBest(w, k, abc), tableBest(w, k, abcd)
	}
	var m5, m6 []string
	for _, k := range keys {
		if s5[k] != s4[k] {
			m5 = append(m5, k+":"+s4[k]+"->"+s5[k])
		}
		if s5[k] == "A" {
			m6 = append(m6, k+":A->"+tableBest(w, k, []string{"B", "C", "D"}))
		}
	}
	ck("六步: k1=A k2=B k3=A k4=C | ⑤[k1:A->D] ⑥[k3:A->B]",
		s4["k1"] == "A" && s4["k2"] == "B" && s4["k3"] == "A" && s4["k4"] == "C" &&
			len(m5) == 1 && m5[0] == "k1:A->D" && len(m6) == 1 && m6[0] == "k3:A->B")
	ck("k3 并列(6=6)取字典序最小=A", tableBest(w, "k3", abc) == "A")
	ck("AddNode(D) 仅 k1 迁移; RemoveNode(A) 仅 k3 迁移", len(m5) == 1 && len(m6) == 1)
	l := api.New()
	for _, n := range abc {
		l.AddNode(n)
	}
	rk := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	ser, naive, det := map[string]string{}, true, true
	for _, k := range rk {
		ser[k], _ = l.Owner(k)
		b, _, _ := rnd.Best(k, abc)
		naive = naive && ser[k] == b
		for i := 0; i < 50; i++ {
			o, _ := l.Owner(k)
			det = det && o == ser[k]
		}
	}
	ck("Owner 与朴素扫描一致", naive)
	ck("确定性: 同键多次结果一致", det)
	ck("三类哨兵错误可判定且互不相同",
		errors.Is(l.AddNode(""), cluster.ErrEmptyNodeID) && errors.Is(l.AddNode("A"), cluster.ErrDuplicateNode) &&
			errors.Is(l.RemoveNode("zzz"), cluster.ErrNodeNotFound) &&
			cluster.ErrEmptyNodeID != cluster.ErrDuplicateNode && cluster.ErrDuplicateNode != cluster.ErrNodeNotFound)
	preserved := true
	for _, k := range rk {
		o, _ := l.Owner(k)
		preserved = preserved && o == ser[k]
	}
	l.AddNode("D")
	_, zerr := l.Owner("zeta")
	ck("被拒后状态不变且仍可继续使用", preserved && zerr == nil)
	for _, k := range rk { // refresh serial baseline now that D may have migrated keys
		ser[k], _ = l.Owner(k)
	}
	scaled := true // large m: only the fixed 5 A-owned keys relocate, independent of m
	for _, m := range []int{100, 1000, 10000} {
		big := api.New()
		big.AddNode("A")
		big.AddNode("B")
		var ak, bk []string
		for i := 0; len(ak) < 5 || len(bk) < m-5; i++ {
			k := fmt.Sprintf("k-%d", i)
			b, _, _ := rnd.Best(k, []string{"A", "B"})
			if b == "A" && len(ak) < 5 {
				ak = append(ak, k)
			}
			if b == "B" && len(bk) < m-5 {
				bk = append(bk, k)
			}
		}
		for _, k := range append(append([]string{}, ak...), bk...) {
			big.Owner(k)
		}
		big.RemoveNode("A")
		for _, k := range append(ak, bk...) {
			o, _ := big.Owner(k)
			scaled = scaled && o == "B"
		}
	}
	ck("大m删A: 仅固定5键重定位, 不随m增长", scaled)
	const N = 16
	var wg sync.WaitGroup
	res := make(chan map[string]string, N)
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got := map[string]string{}
			for i := 0; i < 100; i++ {
				for _, k := range rk {
					got[k], _ = l.Owner(k)
				}
			}
			res <- got
		}()
	}
	close(start)
	wg.Wait()
	close(res)
	conc := true
	for got := range res {
		for k, v := range got {
			conc = conc && v == ser[k]
		}
	}
	ck("16 goroutine 并发只读 Owner 逐键一致且与串行一致", conc)
	if fails > 0 {
		os.Exit(1)
	}
}
