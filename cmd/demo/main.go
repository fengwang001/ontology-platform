package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/jstate"
	"ontology/ljoin"
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

func ch(s ljoin.Side, o ljoin.Op, id, k string) ljoin.Change {
	return ljoin.Change{Side: s, Op: o, ID: id, Key: k}
}
func ou(p bool, l, r string) ljoin.Out { return ljoin.Out{Plus: p, LID: l, RID: r} }

func nine() []ljoin.Change {
	L, R, I, D := ljoin.L, ljoin.R, ljoin.Ins, ljoin.Del
	return []ljoin.Change{ch(R, I, "r1", "x"), ch(L, I, "l1", "x"), ch(L, I, "l2", "y"), ch(L, I, "l3", "y"),
		ch(R, I, "r2", "y"), ch(R, I, "r3", "y"), ch(R, D, "r2", ""), ch(R, D, "r3", ""), ch(R, D, "r1", "")}
}

// want9 是 NOTES.md 九行表中每步的输出（含第 5、6、8 步的 NULL 撤回与补回）。
var want9 = [][]ljoin.Out{
	{}, {ou(true, "l1", "r1")}, {ou(true, "l2", "")}, {ou(true, "l3", "")},
	{ou(false, "l2", ""), ou(true, "l2", "r2"), ou(false, "l3", ""), ou(true, "l3", "r2")},
	{ou(true, "l2", "r3"), ou(true, "l3", "r3")},
	{ou(false, "l2", "r2"), ou(false, "l3", "r2")},
	{ou(false, "l2", "r3"), ou(true, "l2", ""), ou(false, "l3", "r3"), ou(true, "l3", "")},
	{ou(false, "l1", "r1"), ou(true, "l1", "")},
}

func main() {
	p := ljoin.New(jstate.New(), 100)
	o1, _ := p.ApplyOne(ch(ljoin.R, ljoin.Ins, "r1", "x"))
	o2, _ := p.ApplyOne(ch(ljoin.L, ljoin.Ins, "l1", "x"))
	check("R先于L到达的输出", len(o1) == 0 && slices.Equal(o2, []ljoin.Out{ou(true, "l1", "r1")}))

	j := api.New(100)
	var log []ljoin.Out
	ok := true
	for i, c := range nine() { // 九步逐步对拍九行表
		outs, err := j.Apply([]ljoin.Change{c})
		log = append(log, outs...)
		ok = ok && err == nil && slices.Equal(outs, want9[i])
	}
	check("九步输出与九行表一致(含5/6/8步)", ok)

	cnt, nul, mat := map[api.VRow]int{}, map[string]int{}, map[string]int{}
	ok = true
	for _, o := range log { // 逐前缀：计数 0/1、NULL 互斥
		d := 1
		if !o.Plus {
			d = -1
		}
		r := api.VRow{L: o.LID, R: o.RID}
		cnt[r] += d
		if o.RID == "" {
			nul[o.LID] += d
		} else {
			mat[o.LID] += d
		}
		ok = ok && (cnt[r] == 0 || cnt[r] == 1) && !(nul[o.LID] > 0 && mat[o.LID] > 0)
	}
	check("日志每前缀自洽且NULL互斥", ok)

	j2 := api.New(2)
	j2.Apply([]ljoin.Change{ch(ljoin.L, ljoin.Ins, "l1", "x"), ch(ljoin.R, ljoin.Ins, "r1", "x")})
	cases := map[ljoin.Change]error{
		ch(ljoin.L, ljoin.Ins, "", "x"):   ljoin.ErrEmptyField,
		ch(ljoin.L, ljoin.Ins, "l1", "y"): ljoin.ErrDupID,
		ch(ljoin.R, ljoin.Del, "rx", ""):  ljoin.ErrNoID,
		ch(ljoin.R, ljoin.Ins, "r9", "x"): ljoin.ErrTooMany,
	}
	ok = len(cases) == 4
	for cg, want := range cases {
		_, err := j2.Apply([]ljoin.Change{cg})
		ok = ok && errors.Is(err, want)
	}
	sents := []error{ljoin.ErrEmptyField, ljoin.ErrDupID, ljoin.ErrNoID, ljoin.ErrTooMany}
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			ok = ok && !errors.Is(a, b) && a != b
		}
	}
	check("四类可判定错误互不相同", ok)

	j4 := api.New(10)
	j4.Apply([]ljoin.Change{ch(ljoin.L, ljoin.Ins, "l1", "x"), ch(ljoin.R, ljoin.Ins, "r1", "x")})
	before := j4.View()
	bad := []ljoin.Change{ch(ljoin.L, ljoin.Ins, "l2", "x"), ch(ljoin.L, ljoin.Ins, "l2", "z")}
	_, err := j4.Apply(bad)
	ok = err != nil && maps.Equal(j4.View(), before)
	_, err = j4.Apply([]ljoin.Change{ch(ljoin.L, ljoin.Ins, "l2", "x")})
	check("被拒批次不留痕且可继续", ok && err == nil)

	ok = true
	for _, m := range []int{100, 10000} { // 输出条数不随 m 增长（按键定位）
		jm := api.New(10 * m)
		var batch []ljoin.Change
		for i := 0; i < m; i++ {
			k := fmt.Sprintf("k%d", i)
			batch = append(batch, ch(ljoin.L, ljoin.Ins, "l"+k, k), ch(ljoin.R, ljoin.Ins, "r"+k, k))
		}
		_, e1 := jm.Apply(batch)
		a1, e2 := jm.Apply([]ljoin.Change{ch(ljoin.L, ljoin.Ins, "lz", "z")})
		a2, e3 := jm.Apply([]ljoin.Change{ch(ljoin.R, ljoin.Ins, "rz", "z")})
		ok = ok && e1 == nil && e2 == nil && e3 == nil && len(a1) == 1 && len(a2) == 2
	}
	check("大m下检查行数有界(见ljoin测试)", ok)

	j3 := api.New(100)
	j3.Apply(nine())
	want := j3.View()
	var badRead atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				if !maps.Equal(j3.View(), want) || j3.SelfCheck() != nil {
					badRead.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	check("并发只读视图一致", !badRead.Load())
	check("SelfCheck 通过", j.SelfCheck() == nil && api.New(10).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
