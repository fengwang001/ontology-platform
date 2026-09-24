// Command demo 判定两级 LWW-Map CRDT：八步轨迹、墓碑传播、LWW、错误、不留痕、O(1) 定位与并发只读；全 OK 退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/lww"
	"ontology/omap"
)

var failed bool

func report(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}
func op(o, i string, v, ts int64, r string) omap.Op {
	return omap.Op{Kind: "put", O: o, I: i, V: v, TS: ts, Rep: r}
}
func del(o string, ts int64) omap.Op { return omap.Op{Kind: "del", O: o, TS: ts} }
func enc(m map[string]int64) string {
	if len(m) == 0 {
		return "-"
	}
	ks := make([]string, 0, len(m))
	for k, v := range m {
		ks = append(ks, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}
func main() {
	// NOTES.md 八步：side=A/B 独立演进，side=0 表示第 6 步 Merge(A,B)。
	type step struct {
		side  byte
		i, r  string
		v, ts int64
		isDel bool
	}
	steps := []step{
		{'A', "k1", "A", 100, 1, false}, {'A', "k2", "A", 200, 2, false},
		{'A', "", "", 0, 3, true}, {'B', "k3", "B", 300, 1, false},
		{'B', "k4", "B", 400, 2, false}, {0, "", "", 0, 0, false},
		{'A', "k5", "C", 500, 3, false}, {'A', "k6", "D", 600, 4, false},
	}
	A, B := lww.New(), lww.New()
	tombs, vis := []int64{}, []string{}
	for _, s := range steps {
		m := A
		if s.side == 'B' {
			m = B
		}
		var err error
		if s.side == 0 {
			A.Merge(B)
		} else if s.isDel {
			err = m.DelOuter("o", s.ts)
		} else {
			err = m.Put("o", s.i, s.v, s.ts, s.r)
		}
		if err != nil {
			panic(err)
		}
		tombs = append(tombs, m.Tomb("o"))
		vis = append(vis, enc(m.View()["o"]))
	}
	wantT := []int64{0, 0, 3, 0, 0, 3, 3, 3}
	wantV := []string{"k1=100", "k1=100,k2=200", "-", "k3=300", "k3=300,k4=400", "-", "-", "k6=600"}
	ok8 := reflect.DeepEqual(tombs, wantT) && reflect.DeepEqual(vis, wantV)
	fmt.Printf("八步 tomb=%v 可见=%v: %s\n", tombs, vis, map[bool]string{true: "OK", false: "FAIL"}[ok8])
	if !ok8 {
		failed = true
	}
	// 墓碑传播：A 删@3 盖住 B 的 @1/@2，双向合并后 "o" 在两侧都消失。
	X, Y := api.New(), api.New()
	_ = X.Apply(op("o", "k1", 1, 1, "A"))
	_ = X.Apply(del("o", 3))
	_ = Y.Apply(op("o", "k3", 3, 1, "B"))
	_ = Y.Apply(op("o", "k4", 4, 2, "B"))
	X.Merge(Y)
	Y.Merge(X)
	_, xv := X.View()["o"]
	_, yv := Y.View()["o"]
	report("墓碑传播(双向合并后 o 消失)", !xv && !yv)
	// 键级 LWW：ts=5 的 100 胜 ts=2 的 999，与到达顺序无关，双向收敛。
	P, Q := api.New(), api.New()
	_ = P.Apply(op("o", "k", 100, 5, "A"))
	_ = P.Apply(op("o", "k", 999, 2, "B"))
	_ = Q.Apply(op("o", "k", 999, 2, "B"))
	_ = Q.Apply(op("o", "k", 100, 5, "A"))
	P.Merge(Q)
	Q.Merge(P)
	report("键级LWW收敛(k=100,两副本一致)", P.View()["o"]["k"] == 100 && reflect.DeepEqual(P.View(), Q.View()))
	// 三类互异哨兵错误；被拒后无痕迹且实例仍可用。
	Z := api.New()
	e1 := Z.Apply(op("", "i", 1, 1, "R"))
	e2 := Z.Apply(op("o", "", 1, 1, "R"))
	e3 := Z.Apply(op("o", "i", 1, 0, "R"))
	report("三类哨兵错误可判定且互异", errors.Is(e1, lww.ErrEmptyOuter) && errors.Is(e2, lww.ErrEmptyInner) &&
		errors.Is(e3, lww.ErrNonPositiveTS) && e1 != e2 && e2 != e3)
	_ = Z.Apply(op("o", "i", 7, 1, "R"))
	report("被拒后状态不变且仍可用", len(Z.View()) == 1 && Z.View()["o"]["i"] == 7)
	// O(1) 外层定位功能验证；计数器恒 1 由白盒测试 TestOuterLookupConstant 钉住。
	ok := true
	for _, n := range []int{100, 1000, 10000} {
		a := api.New()
		for j := 0; j < n; j++ {
			_ = a.Apply(op(fmt.Sprintf("o%d", j), "i", int64(j), 1, "R"))
		}
		if err := a.Apply(op("known", "i", 9, 1, "R")); err != nil || a.View()["known"]["i"] != 9 {
			ok = false
		}
	}
	report("大m(100/1k/1w)定位正确;计数器恒1见TestOuterLookupConstant", ok)
	// 并发只读：16 goroutine 各取 100 次视图，与基准逐字段相同。
	full := api.New()
	for j := 0; j < 200; j++ {
		_ = full.Apply(op(fmt.Sprintf("o%d", j%20), fmt.Sprintf("i%d", j), int64(j), int64(j)+1, "R"))
	}
	base := full.View()
	var drift atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				if !reflect.DeepEqual(full.View(), base) {
					drift.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	report("16goroutine并发只读逐字段一致", !drift.Load())
	report("SelfCheck四条不变量", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
