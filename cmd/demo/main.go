package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
)

var bad bool

func check(name string, cond bool, extra string) {
	fmt.Printf("%-16s %s %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[cond], extra)
	bad = bad || !cond
}

type st struct {
	tab  string
	x, y int
	del  bool
}

var steps = [8]st{{"S", 1, 10, false}, {"T", 10, 100, false}, {"R", 5, 1, false}, {"S", 1, 10, false}, {"R", 6, 1, false}, {"R", 5, 1, true}, {"S", 1, 10, true}, {"S", 1, 10, true}}

func do(v *api.API, s st) error {
	if s.del {
		return v.Delete(s.tab, s.x, s.y)
	}
	return v.Insert(s.tab, s.x, s.y)
}
func rep(v *api.API, tab string, k, x, y int) {
	for i := 0; i < k; i++ {
		_ = v.Insert(tab, x, y)
	}
}
func recompute(mm map[string]map[[2]int]int) api.View {
	o := api.View{}
	for r, cr := range mm["R"] {
		for s, cs := range mm["S"] {
			if r[1] != s[0] || cr == 0 || cs == 0 {
				continue
			}
			for t, ct := range mm["T"] {
				if s[1] == t[0] && ct > 0 {
					o[[4]int{r[0], r[1], s[1], t[1]}] += cr * cs * ct
				}
			}
		}
	}
	return o
}

func main() {
	v := api.New(0)
	cnt, snaps := [8]int{}, [8]api.View{}
	for i := range steps {
		_ = do(v, steps[i])
		snaps[i] = v.Result()
		for _, c := range snaps[i] {
			cnt[i] += c
		}
	}
	q5, q6 := [4]int{5, 1, 10, 100}, [4]int{6, 1, 10, 100}
	want := [...]api.View{{}, {}, {q5: 1}, {q5: 2}, {q5: 2, q6: 2}, {q6: 2}, {q6: 1}, {}}
	check("八步条数与多重集",
		cnt == [8]int{0, 0, 1, 2, 4, 2, 1, 0} && reflect.DeepEqual(snaps, want), fmt.Sprint(cnt))
	_ = v.Insert("S", 1, 10) // 八步后 R 仅(6,1)、S 空；补 1 份 S 应只连出 q6×1
	check("放大/级联/S剩1", cnt[4]-cnt[5] == 2 && cnt[5]-cnt[6] == 1 && v.Result()[q6] == 1,
		"step4x2; step6删2; step7删1; S剩1份")
	check("批量重连一致", randomEq(), "")
	mm := api.New(0)
	rep(mm, "R", 2, 5, 1)
	rep(mm, "S", 3, 1, 10)
	_ = mm.Insert("T", 10, 100)
	check("多重集语义", mm.Result()[q5] == 6, "R2xS3xT1=6")
	e1, e2 := api.New(0).Insert("Q", 1, 1), api.New(0).Delete("R", 1, 1)
	z := api.New(1)
	_ = z.Insert("S", 1, 2)
	_ = z.Insert("T", 2, 3)
	_ = z.Insert("R", 1, 1)
	e3, snap := z.Insert("R", 2, 1), z.Result()
	_ = z.Insert("R", 2, 1) // 再被拒：结果须与 snap 完全一致
	check("三类错误/原子拒绝", errors.Is(e1, api.ErrBadTable) && errors.Is(e2, api.ErrNotFound) &&
		errors.Is(e3, api.ErrTooMany) && e1 != e2 && e2 != e3 && reflect.DeepEqual(z.Result(), snap), "")
	big := true
	for _, n := range [...]int{100, 1000, 10000} {
		b := api.New(0)
		for i := 0; i < n; i++ {
			_ = b.Insert("S", i, i)
			_ = b.Insert("T", n+i, n+i)
		}
		big = big && b.Insert("R", 0, 0) == nil && len(b.Result()) == 0
	}
	check("大m索引查空", big, "m=100..10000 均0匹配")
	check("并发结果一致", concEq(), "")
	check("SelfCheck", api.New(0).SelfCheck() == nil, "")
	if bad {
		os.Exit(1)
	}
}
func randomEq() bool {
	v, ref := api.New(0), map[string]map[[2]int]int{"R": {}, "S": {}, "T": {}}
	x := 7
	rnd := func(n int) int { x = (x*1103515245 + 12345) & 0x7fffffff; return x % n }
	tabs := [3]string{"R", "S", "T"}
	for i := 0; i < 400; i++ {
		tab, k := tabs[rnd(3)], [2]int{rnd(5), rnd(5)}
		if rnd(3) == 0 && ref[tab][k] > 0 {
			if e := v.Delete(tab, k[0], k[1]); e != nil {
				return false
			}
			ref[tab][k]--
		} else {
			if e := v.Insert(tab, k[0], k[1]); e != nil {
				return false
			}
			ref[tab][k]++
		}
		if !reflect.DeepEqual(v.Result(), recompute(ref)) {
			return false
		}
	}
	return true
}
func concEq() bool {
	var list []st
	for i := 0; i < 50; i++ {
		list = append(list, st{"R", i, 1, false}, st{"S", 1, 7, false}, st{"T", 7, i, false})
	}
	ser := api.New(0)
	for _, s := range list {
		_ = do(ser, s)
	}
	par := api.New(0)
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Go(func() {
			for i := g; i < len(list); i += 10 {
				_ = do(par, list[i])
			}
		})
	}
	wg.Wait()
	return reflect.DeepEqual(par.Result(), ser.Result())
}
