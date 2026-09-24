// Command demo 校验广播状态规则版本化的关键性质，逐条打印 OK/FAIL（≤10 行），退出码反映结果。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/inst"
	"ontology/rule"
)

var fail bool

func ck(n string, ok bool) {
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok], n)
	if !ok {
		fail = true
	}
}
func H(k, v int64, id string, ver, i int) inst.Hit {
	return inst.Hit{Key: k, Val: v, RuleID: id, Ver: ver, Inst: i}
}
func put(id string, th int64) rule.Update { return rule.Put{ID: id, Threshold: th} }
func del(id string) rule.Update           { return rule.Delete{ID: id} }

func main() {
	st := [][5]int{{1, 0, 0, 0, 0}, {1, 1, 0, 0, 0}, {1, 1, 0, 0, 0}, {1, 1, 0, 0, 1},
		{2, 1, 0, 0, 1}, {3, 1, 0, 0, 1}, {3, 1, 0, 0, 2}, {3, 1, 0, 1, 2},
		{3, 1, 3, 1, 0}, {3, 2, 3, 1, 0}, {3, 2, 3, 2, 0}, {3, 3, 3, 0, 0}}
	add := [][]inst.Hit{nil, nil, {H(0, 15, "r1", 1, 0)}, nil, nil, nil, nil, nil,
		{H(1, 12, "r1", 1, 1), H(1, 8, "r2", 3, 1)}, nil, nil,
		{H(0, 20, "r2", 3, 0), H(2, 7, "r2", 3, 0)}}
	a := api.New(2, 8)
	acts := []func(){
		func() { _ = a.Publish(put("r1", 10)) }, func() { _ = a.Deliver(0, 1) }, func() { _ = a.Send(0, 15) }, func() { _ = a.Send(1, 12) }, func() { _ = a.Publish(put("r2", 5)) }, func() { _ = a.Publish(del("r1")) },
		func() { _ = a.Send(1, 8) }, func() { _ = a.Send(0, 20) }, func() { _ = a.Deliver(1, 3) }, func() { _ = a.Deliver(0, 1) }, func() { _ = a.Send(2, 7) }, func() { _ = a.Deliver(0, 1) }}
	ok12 := true
	var acc []inst.Hit
	for i, do := range acts {
		do()
		acc = append(acc, add[i]...)
		G, vs, bs := a.State()
		if [5]int{G, vs[0], vs[1], bs[0], bs[1]} != st[i] || !reflect.DeepEqual(a.Output(), acc) {
			ok12 = false
		}
	}
	ck("十二步逐步 版本/缓冲/命中 全对", ok12)
	ck("第9步按tag快照 (1,12)->r1,Ver1", a.Output()[1] == H(1, 12, "r1", 1, 1))
	r := rand.New(rand.NewSource(1))
	okRnd := true
	seq := []func(*api.API){
		func(z *api.API) { _ = z.Publish(put("r1", 10)) }, func(z *api.API) { _ = z.Send(0, 15) }, func(z *api.API) { _ = z.Send(1, 12) }, func(z *api.API) { _ = z.Publish(put("r2", 5)) },
		func(z *api.API) { _ = z.Publish(del("r1")) }, func(z *api.API) { _ = z.Send(1, 8) }, func(z *api.API) { _ = z.Send(0, 20) }, func(z *api.API) { _ = z.Send(2, 7) }}
	for t := 0; t < 50 && okRnd; t++ {
		z := api.New(2, 64)
		for _, o := range seq {
			o(z)
			G, vs := z.Versions()
			for i := 0; i < 2; i++ {
				if d := G - vs[i] - r.Intn(G-vs[i]+1); d > 0 {
					_ = z.Deliver(i, d)
				}
				_, vs = z.Versions()
			}
		}
		G, vs := z.Versions()
		for i := 0; i < 2; i++ {
			_ = z.Deliver(i, G-vs[i])
		}
		g := z.Output()
		if !inst.EqualSet(g, a.Output()) || !reflect.DeepEqual(inst.ByInst(g, 0), inst.ByInst(a.Output(), 0)) || !reflect.DeepEqual(inst.ByInst(g, 1), inst.ByInst(a.Output(), 1)) {
			okRnd = false
		}
	}
	ck("随机投递穿插 集合与顺序不变", okRnd)
	want := []inst.Hit{H(0, 15, "r1", 1, 0), H(1, 12, "r1", 1, 1), H(1, 8, "r2", 3, 1), H(0, 20, "r2", 3, 0), H(2, 7, "r2", 3, 0)}
	ck("与朴素参照一致 + SelfCheck", inst.EqualSet(a.Output(), want) && api.New(1, 1).SelfCheck())
	ck("四类哨兵错误 互不相同",
		api.ErrIllegalRule != api.ErrInvalidDeliver && api.ErrIllegalRule != api.ErrInvalidKey && api.ErrIllegalRule != api.ErrBufferFull &&
			api.ErrInvalidDeliver != api.ErrInvalidKey && api.ErrInvalidDeliver != api.ErrBufferFull && api.ErrInvalidKey != api.ErrBufferFull)
	b := api.New(2, 8)
	_ = b.Publish(put("r1", 10))
	G0, v0 := b.Versions()
	rej := b.Publish(del("none")) == api.ErrIllegalRule && b.Deliver(0, 2) == api.ErrInvalidDeliver && b.Send(-1, 1) == api.ErrInvalidKey
	G1, v1 := b.Versions()
	keep := b.Deliver(0, 1) == nil && b.Send(0, 15) == nil
	ck("拒绝不留痕 且可继续", rej && G0 == G1 && reflect.DeepEqual(v0, v1) && keep && len(b.Output()) == 1)
	const m = 5000
	c := api.New(1, m*2)
	for i := 0; i < 4; i++ {
		_ = c.Publish(put(fmt.Sprintf("r%d", i), 1))
	}
	for i := 0; i < m; i++ {
		_ = c.Send(int64(i), 10)
	}
	_ = c.Deliver(0, 1)
	_, vm, bm := c.State()
	_ = c.Deliver(0, 3)
	ck("大m 早版本不刷tag=G 末版本恰好刷出", vm[0] == 1 && bm[0] == m && len(c.Output()) == m*4)
	d := api.New(4, 1<<20)
	for _, u := range []rule.Update{put("r0", 1), put("r1", 100), del("r1"), put("r2", 3), put("r0", 2)} {
		_ = d.Publish(u)
	}
	mono := true
	stop := make(chan struct{})
	var wm, ww sync.WaitGroup
	wm.Add(1)
	go func() {
		defer wm.Done()
		prev := []int{0, 0, 0, 0}
		for {
			select {
			case <-stop:
				return
			default:
				_, vs := d.Versions()
				for i := range vs {
					if vs[i] < prev[i] {
						mono = false
					}
					prev[i] = vs[i]
				}
			}
		}
	}()
	for i := 0; i < 4; i++ {
		ww.Add(1)
		go func(i int) { defer ww.Done(); _ = d.Deliver(i, 5) }(i)
	}
	var exp []inst.Hit
	for k := int64(0); k < 400; k++ {
		v := k % 6
		_ = d.Send(k, v)
		if v >= 2 {
			exp = append(exp, H(k, v, "r0", 5, int(k%4)))
		}
		if v >= 3 {
			exp = append(exp, H(k, v, "r2", 5, int(k%4)))
		}
	}
	ww.Wait()
	close(stop)
	wm.Wait()
	ck("并发投递+发送 同参照且v单调", mono && inst.EqualSet(d.Output(), exp))
	os.Exit(map[bool]int{true: 1, false: 0}[fail])
}
