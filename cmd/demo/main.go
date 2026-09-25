// Command demo 以 <=10 行输出逐条演示 Marzullo 算法判定；退出码 0 表示全部 OK。
package main

import (
	"errors"
	"fmt"
	"math/bits"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/csync"
	"ontology/marz"
)

var fail int

func check(name string, ok bool, detail string) {
	if !ok {
		fail++
	}
	tag := "OK  "
	if !ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %-14s %s\n", tag, name, detail)
}

// readProbes 直接读非导出字段 probes（不经任何导出函数/方法，计数器不进公开接口）。
func readProbes(s *csync.Set) int {
	p := reflect.ValueOf(s).Elem().FieldByName("probes")
	return *(*int)(unsafe.Pointer(p.UnsafeAddr()))
}

type lab struct {
	p int64
	d int
	w string
}

func main() {
	base := []struct {
		id      string
		off, er int64
	}{{"A", 10, 2}, {"B", 11, 1}, {"C", 20, 1}}
	// 1) 六行扫描线：端点生成/排序由 marz 完成，标签按相同稳定顺序对齐；ev 供朴素重算。
	var es []lab
	ev := make([]marz.Event, 0, 6)
	for _, c := range base {
		l, r, _ := marz.MakeEvents(c.off, c.er)
		es = append(es, lab{l.Pos, 1, c.id}, lab{r.Pos, -1, c.id})
		ev = append(ev, l, r)
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].p < es[j].p || es[i].p == es[j].p && es[i].d > es[j].d })
	count, in := 0, false
	steps := ""
	for _, e := range es {
		prev := count
		count += e.d
		trans := ""
		if !in && prev < 2 && count >= 2 {
			in, trans = true, fmt.Sprintf("in[%d", e.p)
		} else if in && count < 2 {
			in, trans = false, fmt.Sprintf("out%d]", e.p)
		}
		steps += fmt.Sprintf("%s%d(%s):%d%s ", map[int]string{1: "L", -1: "R"}[e.d], e.p, e.w, count, trans)
	}
	check("scan", steps == "L8(A):1 L10(B):2in[10 R12(A):1out12] R12(B):0 L19(C):1 R21(C):0 ", steps)
	// 2-5) 共识 / CountAt / 朴素重算一致 / 边界闭合。
	a, _ := api.New(1)
	for _, c := range base {
		_ = a.Add(c.id, c.off, c.er)
	}
	lo, hi, err := a.Consensus()
	marz.SortEvents(ev)
	nlo, nhi, _ := marz.Sweep(ev, 2) // 取当前全部时钟重新跑一遍扫描线
	check("consensus", err == nil && lo == 10 && hi == 12, fmt.Sprintf("[%d,%d]", lo, hi))
	check("countAt", a.CountAt(11) == 2 && a.CountAt(9) == 1,
		fmt.Sprintf("11->%d 9->%d", a.CountAt(11), a.CountAt(9)))
	check("naive+bound", nlo == lo && nhi == hi && a.CountAt(lo) >= 2 && a.CountAt(hi) >= 2,
		fmt.Sprintf("naive[%d,%d] ends=%d,%d", nlo, nhi, a.CountAt(lo), a.CountAt(hi)))

	// 6-7) 四类互不相同的哨兵错误，且被拒后状态不变、仍可继续使用。
	s, _ := csync.NewSet(1)
	_ = s.Add(csync.Clock{ID: "X", Offset: 0, Err: 1})
	overF, _ := csync.NewSet(2) // f>=K：需两台时钟才能越过 k<2 判定
	_ = overF.Add(csync.Clock{ID: "p"})
	_ = overF.Add(csync.Clock{ID: "q"})
	one, _ := csync.NewSet(0) // 仅一台：时钟不足
	_ = one.Add(csync.Clock{ID: "p"})
	consErr := func(x *csync.Set) error { _, _, e := x.Consensus(); return e }
	_, eNew := csync.NewSet(-1)
	got := []error{s.Add(csync.Clock{ID: "Y", Err: -1}), s.Add(csync.Clock{ID: "X", Err: 0}), eNew, consErr(one)}
	want := []error{api.ErrNegativeError, api.ErrDuplicateID, api.ErrInvalidF, api.ErrNoConsensus}
	distinct := errors.Is(consErr(overF), api.ErrInvalidF) // f<0 与 f>=K 同一哨兵
	seen := map[error]bool{}
	for i, g := range got {
		if !errors.Is(g, want[i]) || seen[g] {
			distinct = false
		}
		seen[g] = true
	}
	if e := s.Add(csync.Clock{ID: "Z", Offset: 0, Err: 2}); e != nil {
		distinct = false
	}
	check("errors", distinct && s.Len() == 2, "4 distinct sentinels; rejected adds left no trace")
	// 8) 大 m：probes 受 O(log m) 上界约束（白盒读取非导出字段）。
	var ms, ps []int
	logOK := true
	for _, m := range []int{100, 1000, 10000} {
		big, _ := csync.NewSet(1)
		r := rand.New(rand.NewSource(int64(m)))
		for i := 0; i < m; i++ {
			_ = big.Add(csync.Clock{ID: fmt.Sprintf("c%d", i), Offset: r.Int63n(1 << 40), Err: r.Int63n(1000)})
		}
		_ = big.CountAt(1 << 30)
		ms, ps = append(ms, m), append(ps, readProbes(big))
		if ps[len(ps)-1] > 4*bits.Len(uint(m))+2 {
			logOK = false
		}
	}
	check("probes-log", logOK && ps[2] < 100, fmt.Sprintf("m=%v probes=%v", ms, ps))
	// 9) 并发只读：N 个 goroutine 的结果全部一致；另验证 SelfCheck。
	const n = 32
	var wg sync.WaitGroup
	res := make([][3]int64, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			l, h, _ := a.Consensus()
			res[g] = [3]int64{l, h, int64(a.CountAt(11))}
		}(g)
	}
	wg.Wait()
	same := true
	for _, r := range res {
		if r != res[0] {
			same = false
		}
	}
	check("concurrent", same, fmt.Sprintf("%d readers agree %v; selfcheck=%v", n, res[0], a.SelfCheck()))
	if fail > 0 {
		os.Exit(1)
	}
}
