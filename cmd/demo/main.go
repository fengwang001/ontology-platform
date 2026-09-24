// Command demo 逐条打印保留首条去重的判定 OK/FAIL；任一 FAIL 退出码非 0，输出 ≤10 行。
package main

import "errors"
import "fmt"
import "os"
import "reflect"
import "strconv"
import "sync"
import "ontology/api"
import "ontology/rank"

type C = api.Change
type O = api.Out

func ins(id, k string, t int64) C { return C{Op: '+', ID: id, Key: k, T: t} }
func del(id string) C             { return C{Op: '-', ID: id} }
func enc(os []O) string {
	if len(os) == 0 {
		return "_"
	}
	s := ""
	for _, e := range os {
		s += string(e.Op) + e.ID
	}
	return s
}
func naive(l map[string]O) map[string]O {
	m := map[string]O{}
	for _, r := range l {
		if c, ok := m[r.Key]; !ok || r.T < c.T || r.T == c.T && r.ID < c.ID {
			m[r.Key] = r
		}
	}
	return m
}
func gen(n int, sd uint64) []C { // 演示用确定性 LCG：多 Key、负 T、撤后同 ID 再插
	x, nid := sd, 0
	a, d, cs := []string{}, []string{}, []C{}
	for len(cs) < n {
		x = x*6364136223846793005 + 1442695040888963407
		r := x % 5
		if r >= 3 && len(a) > 0 {
			id := a[0]
			a, d, cs = a[1:], append(d, id), append(cs, del(id))
			continue
		}
		id := ""
		if r == 0 && len(d) > 0 {
			i := x % uint64(len(d))
			id, d = d[i], append(d[:i:i], d[i+1:]...)
		} else {
			id, nid = "id"+strconv.Itoa(nid), nid+1
		}
		cs = append(cs, ins(id, "k"+strconv.Itoa(int(x%5)), int64(x%201)-100))
		a = append(a, id)
	}
	return cs
}
func main() {
	failed := false
	chk := func(name string, ok bool) {
		tag := "OK"
		if !ok {
			tag, failed = "FAIL", true
		}
		fmt.Println(tag, name)
	}
	seq := []C{ins("a", "k", 5), ins("b", "k", 8), ins("p", "k", 3), ins("m", "k", 3),
		del("m"), ins("e", "k", 1), del("b"), del("e"), del("p")}
	want := []string{"+a", "_", "-a+p", "-p+m", "-m+p", "-p+e", "_", "-e+p", "-p+a"}
	v, got, v456 := api.New(0), make([]string, 9), ""
	for i, c := range seq {
		o, err := v.Apply([]C{c})
		failed = failed || err != nil
		got[i] = enc(o)
		if i >= 3 && i <= 5 {
			v456 += v.View()["k"].ID
		}
	}
	fmt.Println("OK 九步逐步输出:", fmt.Sprint(got))
	chk("第4/5/6步输出 -p+m/-m+p/-p+e、视图 m/p/e", reflect.DeepEqual(got, want) && v456 == "mpe")
	vt := api.New(0)
	_, _ = vt.Apply([]C{ins("b", "k", 3)})
	ot, _ := vt.Apply([]C{ins("a", "k", 3)})
	chk("T 相等按 ID 字节字典序打破并列", enc(ot) == "-b+a" && vt.View()["k"].ID == "a")
	vr, lives, cur := api.New(0), map[string]O{}, map[string]O{}
	pref, mini := true, true
	for _, c := range gen(160, 20260924) {
		before := naive(lives)
		o, err := vr.Apply([]C{c})
		failed = failed || err != nil
		delete(lives, c.ID)
		if c.Op == '+' {
			lives[c.ID] = O{Op: '+', Key: c.Key, ID: c.ID, T: c.T}
		}
		mini = mini && len(o) <= 2 && ((len(o) == 0) == reflect.DeepEqual(before, naive(lives)))
		for _, e := range o {
			g, ok := cur[e.Key]
			if e.Op == '-' && (!ok || g.ID != e.ID || g.T != e.T) || e.Op == '+' && ok {
				pref = false
			}
			if e.Op == '-' {
				delete(cur, e.Key)
			} else {
				cur[e.Key] = e
			}
		}
	}
	chk("随机序列后视图与朴素批量结果一致", reflect.DeepEqual(vr.View(), naive(lives)))
	chk("变更日志每个前缀自洽且输出最小(0/1/2)", pref && mini && reflect.DeepEqual(cur, naive(lives)))
	v0, v1 := api.New(0), api.New(1)
	_, _ = v1.Apply([]C{ins("a", "k", 1)})
	e := func(v *api.API, b []C) error { _, x := v.Apply(b); return x }
	four := errors.Is(e(v0, []C{ins("", "k", 1)}), api.ErrInvalidChange) &&
		errors.Is(e(v1, []C{ins("a", "k", 9)}), api.ErrIDExists) &&
		errors.Is(e(v0, []C{del("x")}), api.ErrIDMissing) &&
		errors.Is(e(v1, []C{ins("b", "q", 1)}), api.ErrRowLimit)
	chk("四类可判定哨兵错误互不相同", four)
	snap := v1.View()
	_, e6 := v1.Apply([]C{del("a"), ins("b", "q", 1), ins("c", "r", 1)})
	unchanged := errors.Is(e6, api.ErrRowLimit) && reflect.DeepEqual(snap, v1.View())
	_, eUse := v1.Apply([]C{del("a"), ins("d", "k", 2)})
	chk("被拒整批状态不变且拒绝后仍可正常使用", unchanged && eUse == nil)
	chk("大 m 下插最大行/撤首条比较次数不随 m 线性增长", rank.ComplexityCheck())
	vc := api.New(0)
	for _, c := range gen(120, 77) {
		_, _ = vc.Apply([]C{c})
	}
	ref := vc.View()
	var wg sync.WaitGroup
	bad := make(chan struct{}, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if !reflect.DeepEqual(vc.View(), ref) || vc.SelfCheck() != nil {
					bad <- struct{}{}
					return
				}
			}
		}()
	}
	wg.Wait()
	chk("并发只读 View/SelfCheck 结果逐 Key 相同", len(bad) == 0)
	if failed {
		os.Exit(1)
	}
}
