package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/part"
	"ontology/store"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[ok] + name)
}

func fmtRanges(rs []part.Range) string {
	ss := make([]string, len(rs))
	for i, r := range rs {
		ss[i] = fmt.Sprintf("[%d,%d):%d", r.Lo, r.Hi, r.Load)
	}
	return strings.Join(ss, " ")
}

// invariantsOK 核验范围不变量、键不丢不重、load 与朴素重算一致。
func invariantsOK(s *store.Store, live map[int64]bool) bool {
	rs := s.Ranges()
	if rs[0].Lo != 0 || rs[len(rs)-1].Hi != 16 {
		return false
	}
	total := 0
	for i, r := range rs {
		if r.Lo >= r.Hi || (i > 0 && rs[i-1].Hi != r.Lo) {
			return false
		}
		n := 0
		for k := range live {
			if r.Lo <= k && k < r.Hi {
				n++
			}
		}
		if n != r.Load {
			return false
		}
		total += r.Load
	}
	return total == len(live)
}

func main() {
	p := part.New(0, 16)
	for _, k := range []int64{5, 11, 8} {
		p.Add(k)
	}
	l, r := p.Split()
	check("part: mid=8 键8归右 [8,16)load=2", l.Load() == 1 && r.Load() == 2 && r.Has(8) && !l.Has(8))

	s, _ := store.New(0, 16, 3, 2)
	live := map[int64]bool{}
	inv := true
	states := []string{}
	ops := []struct{ ins, del int64 }{{5, -1}, {11, -1}, {8, -1}, {14, -1}, {2, -1}, {6, -1}, {12, -1}, {10, -1}, {-1, 5}, {-1, 2}, {-1, -1}}
	for _, o := range ops {
		switch {
		case o.ins >= 0:
			inv = s.Insert(o.ins) == nil && inv
			live[o.ins] = true
		case o.del >= 0:
			inv = s.Delete(o.del) == nil && inv
			delete(live, o.del)
		default:
			s.Compact()
		}
		inv = invariantsOK(s, live) && inv
		states = append(states, fmtRanges(s.Ranges()))
	}
	fmt.Println("步1-4: " + strings.Join(states[:4], " | "))
	fmt.Println("步5-8: " + strings.Join(states[4:8], " | "))
	fmt.Println("步9-11: " + strings.Join(states[8:], " | "))
	check("判定: 步3键8归右/步8切[8,10)/步11 Compact=3分区",
		states[2] == "[0,8):1 [8,16):2" && states[7] == "[0,4):1 [4,8):2 [8,10):1 [10,12):2 [12,16):2" &&
			states[10] == "[0,10):2 [10,12):2 [12,16):2")
	check("不变量: 不丢不重+范围连续覆盖+load=朴素重算", inv)

	_, e1 := store.New(0, 0, 3, 2)
	s2, _ := store.New(0, 16, 3, 2)
	s2.Insert(5)
	before := fmtRanges(s2.Ranges())
	e4 := s2.Insert(16)
	_, e6 := s2.Locate(99)
	e7 := s2.Delete(3)
	check("三类哨兵互不相同: 参数非法/键越界/键不存在",
		errors.Is(e1, store.ErrInvalidParams) && errors.Is(e4, store.ErrOutOfRange) &&
			errors.Is(e6, store.ErrOutOfRange) && errors.Is(e7, store.ErrNotFound) &&
			!errors.Is(e1, e4) && !errors.Is(e4, e7) && !errors.Is(e1, e7))
	check("失败不留痕: 被拒后状态不变且可继续",
		fmtRanges(s2.Ranges()) == before && s2.Insert(7) == nil && s2.Delete(5) == nil)
	check("大P: Locate比较数 ≤ ceil(log2 P)+1", store.CheckLocateLogBound())

	tb, _ := api.New(0, 1<<22, 4, 2)
	var keys []int64
	for k := int64(0); k < 3000; k++ {
		if key := k*1000 + 7; tb.Insert(key) == nil {
			keys = append(keys, key)
		}
	}
	ref := make([]api.Range, len(keys))
	for i, k := range keys {
		ref[i], _ = tb.Locate(k)
	}
	const G = 8
	got := make([][]api.Range, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got[g] = make([]api.Range, len(keys))
			for i, k := range keys {
				got[g][i], _ = tb.Locate(k)
			}
		}(g)
	}
	wg.Wait()
	conc := tb.SelfCheck() == nil
	for g := 0; g < G; g++ {
		conc = slices.Equal(got[g], ref) && conc
	}
	check("api: SelfCheck + 8goroutine Locate 逐键一致", conc)

	if failed {
		os.Exit(1)
	}
}
