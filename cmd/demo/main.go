// 三维 CUBE 增量维护演示：逐条打印 OK/FAIL，任一 FAIL 退出码非 0。
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

func ok(name string, cond bool, detail ...any) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		bad = true
		fmt.Println("FAIL " + name + " " + fmt.Sprint(detail...))
	}
}

// get 在视图里查一个 cell：all 为 true 表示该维 ALL。
func get(v []api.Cell, a, b, c string, allA, allB, allC bool) (int64, bool) {
	for _, x := range v {
		if x.A == a && x.B == b && x.C == c && x.AllA == allA && x.AllB == allB && x.AllC == allC {
			return x.Sum, true
		}
	}
	return 0, false
}

func main() {
	x, err := api.New(1 << 20)
	if err != nil {
		fmt.Println("FAIL New", err)
		os.Exit(1)
	}
	// 第三节五步：每步核对 (*,*,*) (a,*,*) (*,b,c) (a,b,c)。
	steps := []struct {
		add  bool
		f    api.Fact
		want [4]int64
	}{
		{true, api.Fact{A: "a", B: "b", C: "c", V: 2}, [4]int64{2, 2, 2, 2}},
		{true, api.Fact{A: "a", B: "b", C: "c", V: 3}, [4]int64{5, 5, 5, 5}},
		{true, api.Fact{A: "a", B: "d", C: "c", V: 5}, [4]int64{10, 10, 5, 5}},
		{true, api.Fact{A: "e", B: "b", C: "c", V: 7}, [4]int64{17, 10, 12, 5}},
		{false, api.Fact{A: "a", B: "b", C: "c", V: 2}, [4]int64{15, 8, 10, 3}},
	}
	pass := true
	for _, s := range steps {
		if s.add {
			err = x.Add(s.f)
		} else {
			err = x.Remove(s.f)
		}
		v := x.View()
		tot, _ := get(v, "", "", "", true, true, true)
		ga, _ := get(v, "a", "", "", false, true, true)
		gbc, _ := get(v, "", "b", "c", true, false, false)
		gabc, _ := get(v, "a", "b", "c", false, false, false)
		if err != nil || [4]int64{tot, ga, gbc, gabc} != s.want {
			pass = false
		}
	}
	ok("five-step table", pass)
	v := x.View()
	gc, _ := get(v, "", "", "c", true, true, false)
	gac, _ := get(v, "a", "", "c", false, true, false)
	ok("(甲) (*,*,c)=15 (a,*,c)=8", gc == 15 && gac == 8, gc, gac)
	ok("(丙) 16 non-empty cells", len(v) == 16, len(v))
	if err := x.Add(api.Fact{A: "", B: "b", C: "c", V: 4}); err != nil {
		ok("(乙) add empty-A fact", false, err)
	}
	v = x.View()
	tot, _ := get(v, "", "", "", true, true, true)
	gbc, _ := get(v, "", "b", "c", true, false, false)
	ge, _ := get(v, "", "b", "c", false, false, false)
	ok("(乙) empty-A: total=19 (*,b,c)=14 (\"\",b,c)=4", tot == 19 && gbc == 14 && ge == 4, tot, gbc, ge)

	// 三类可判定错误互不相同，且被拒后状态不变。
	_, e1 := api.New(0)
	z, _ := api.New(7)
	before := fmt.Sprint(z.View())
	e2 := z.Add(api.Fact{A: "1", B: "2", C: "3", V: 1}) // 8 cell > 7，超限
	e3 := z.Remove(api.Fact{A: "9", B: "9", C: "9", V: 1})
	ok("three distinct sentinel errors", errors.Is(e1, api.ErrMaxCells) &&
		errors.Is(e2, api.ErrCellLimit) && errors.Is(e3, api.ErrFactNotFound) &&
		e1 != e2 && e2 != e3 && e1 != e3, e1, e2, e3)
	ok("rejected ops leave no trace", fmt.Sprint(z.View()) == before)

	// 大 m 下每事实触碰 cell 数恒为 8（用全新取值事实带来的新增 cell 数间接验证）。
	big, _ := api.New(1 << 20)
	for i := 0; i < 10000; i++ {
		_ = big.Add(api.Fact{A: fmt.Sprint("a", i%100), B: fmt.Sprint("b", i%37), C: fmt.Sprint("c", i%11), V: 1})
	}
	n0 := len(big.View())
	t0, _ := get(big.View(), "", "", "", true, true, true)
	_ = big.Add(api.Fact{A: "NA", B: "NB", C: "NC", V: 1})
	t1, _ := get(big.View(), "", "", "", true, true, true)
	// 8 次触碰 = 7 个全新 cell + 已存在的总计 cell (*,*,*) 加 V。
	ok("fresh fact touches exactly 8 cells at m=10000", len(big.View())-n0 == 7 && t1-t0 == 1, len(big.View())-n0, t1-t0)

	// 并发只读：N 个 goroutine 各自 View，必须逐 cell 相同。
	want := x.View()
	var wg sync.WaitGroup
	consistent := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				lvlOK := api.Level(api.Cell{AllA: true, AllB: true, AllC: true}) == 0 &&
					api.Level(api.Cell{}) == 3
				if !reflect.DeepEqual(x.View(), want) || !lvlOK || x.SelfCheck() != nil {
					consistent = false
				}
			}
		}()
	}
	wg.Wait()
	ok("concurrent read-only views identical", consistent)
	ok("SelfCheck", x.SelfCheck() == nil)
	if bad {
		os.Exit(1)
	}
}
