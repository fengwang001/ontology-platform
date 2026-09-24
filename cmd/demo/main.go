package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
)

func ok(n string, c bool) {
	if c {
		fmt.Println("OK:", n)
	} else {
		fmt.Println("FAIL:", n)
		os.Exit(1)
	}
}
func U(k int64, v string) api.Entry { return api.Entry{Op: api.Upsert, Key: k, Val: v} }
func D(k int64) api.Entry           { return api.Entry{Op: api.Delete, Key: k} }

// runChunks 顺序跑 n 个不相交 chunk 的三步并在块间/末尾 Poll，边收输出边回放。
func runChunks(a *api.API, n int, hook func(int64)) (map[int64]string, bool) {
	m, safe := map[int64]string{}, true
	add := func(os []api.Out) {
		for _, o := range os {
			if o.Op == api.Upsert {
				m[o.Key] = o.Val
			} else if _, x := m[o.Key]; !x {
				safe = false
			} else {
				delete(m, o.Key)
			}
		}
	}
	for ch := 0; ch < n; ch++ {
		lo := int64(ch * 100)
		hook(lo)
		_ = a.BeginChunk(lo, lo+100)
		_ = a.ReadChunk()
		hook(lo)
		e, _ := a.EndChunk()
		add(e)
		p, _ := a.Poll()
		add(p)
	}
	p, _ := a.Poll()
	add(p)
	return m, safe
}
func section3() (e, p []api.Out, v map[int64]string) {
	a := api.New(100)
	a.Append(U(10, "a"), U(15, "b"), U(20, "x"))
	_ = a.BeginChunk(10, 20)
	a.Append(U(15, "c"), D(10))
	_ = a.ReadChunk()
	a.Append(U(12, "d"), U(15, "e"), U(20, "y"))
	e, _ = a.EndChunk()
	a.Append(U(10, "f"), D(12), U(19, "g"))
	p, _ = a.Poll()
	return e, p, a.View()
}
func interleave(seed int64) (string, string, bool) {
	rng, s := rand.New(rand.NewSource(seed)), api.New(1<<20)
	src := map[int64]string{}
	batch := func(int64) {
		es := make([]api.Entry, rng.Intn(4))
		for i := range es {
			k := int64(rng.Intn(800))
			if rng.Intn(2) == 0 {
				src[k] = "v"
				es[i] = U(k, "v")
			} else {
				delete(src, k)
				es[i] = D(k)
			}
		}
		s.Append(es...)
	}
	rp, safe := runChunks(s, 8, batch)
	return fmt.Sprint(src), fmt.Sprint(rp), safe && fmt.Sprint(rp) == fmt.Sprint(s.View())
}
func bigEnd(m int) string {
	x := api.New(1 << 30)
	for i := 0; i < m; i++ {
		x.Append(U(int64(i), "x"))
	}
	_ = x.BeginChunk(0, 10)
	x.Append(U(1, "p"))
	_ = x.ReadChunk()
	x.Append(U(2, "q"))
	o, _ := x.EndChunk()
	return fmt.Sprint(o)
}
func main() {
	e, p, v := section3()
	ok("L=3 H=8 快照修正 end/poll/最终视图",
		fmt.Sprint(e) == "[{1 12 d} {1 15 e}]" &&
			fmt.Sprint(p) == "[{1 10 f} {2 12 } {1 19 g}]" &&
			fmt.Sprint(v) == "map[10:f 15:e 19:g]")
	ok("边界：键10∈[10,20) 键20∉；SelfCheck 四不变量通过",
		v[10] == "f" && v[20] == "" && api.New(100).SelfCheck() == nil)
	src, rp, safe := interleave(7)
	ok("随机交错下视图与源表一致", src == rp)
	ok("输出不重复不回退（删除时键必存在、前缀即视图）", safe)
	b := api.New(100)
	_ = b.BeginChunk(0, 10)
	set := map[error]bool{
		api.ErrInvalidRange: true, api.ErrOverlapping: true,
		api.ErrStage: true, api.ErrViewTooLarge: true,
	}
	ok("四类可判定且互不相同的错误", len(set) == 4 &&
		errors.Is(b.BeginChunk(1, 1), api.ErrInvalidRange) &&
		errors.Is(b.BeginChunk(20, 30), api.ErrStage) &&
		errors.Is(b.BeginChunk(9, 20), api.ErrOverlapping))
	t := api.New(1)
	t.Append(U(1, "a"), U(2, "b"))
	_ = t.BeginChunk(0, 10)
	_ = t.ReadChunk()
	_, e1 := t.EndChunk()
	ok("被拒后状态不变且仍可继续", errors.Is(e1, api.ErrViewTooLarge) &&
		errors.Is(t.ReadChunk(), api.ErrStage) && len(t.View()) == 0)
	ok("大 m 下修正结果不随 m 增长（100 与 10000 一致）", bigEnd(100) == bigEnd(10000))
	c := api.New(1 << 30)
	stop, wg := make(chan struct{}), sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(1))
		for {
			select {
			case <-stop:
				return
			default:
				c.Append(U(int64(r.Intn(1200)), "z"))
			}
		}
	}()
	rp2, safe2 := runChunks(c, 10, func(int64) {})
	close(stop)
	wg.Wait()
	ok("并发写入下结果正确（-race 干净、输出可回放且前缀即视图）",
		safe2 && fmt.Sprint(rp2) == fmt.Sprint(c.View()))
}
