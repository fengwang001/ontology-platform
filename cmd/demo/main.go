// Command demo exercises the CDC before-image apply API. Exit 0 iff all OK.
package main

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/rapply"
	"ontology/rimg"
)

type E = rimg.Event
type R = rimg.Row

func ok(n string, p bool) (f int) {
	if !p {
		f = 1
	}
	fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[p] + n)
	return
}
func row(s ...string) (r R) {
	r = R{}
	for i := 0; i+1 < len(s); i += 2 {
		r[s[i]] = s[i+1]
	}
	return
}
func ins(s, pk int64, a R) E    { return E{Seq: s, Kind: rimg.Insert, PK: pk, After: a} }
func upd(s, pk int64, b, a R) E { return E{Seq: s, Kind: rimg.Update, PK: pk, Before: b, After: a} }

func eight() (bool, bool) {
	a := api.New(map[int64]R{1: row("name", "ann", "qty", "3"), 2: row("name", "bob", "qty", "5"), 3: row("name", "cid")}, 4)
	evs := []E{
		upd(1, 1, row("name", "ann", "qty", "3"), row("name", "ann", "qty", "4")), upd(2, 2, row("name", "bob", "qty", "6"), row("name", "bob", "qty", "7")),
		{Seq: 3, Kind: rimg.Delete, PK: 3, Before: row("name", "cid", "qty", "")}, ins(4, 2, row("name", "bea", "qty", "1")),
		{Seq: 5, Kind: rimg.Delete, PK: 1, Before: row("name", "ann", "qty", "3")}, upd(6, 4, row("name", "dan"), row("name", "dan", "qty", "2")),
		ins(7, 4, row("name", "dan", "qty", "2")), upd(8, 4, row("name", "dan", "qty", "2"), row("name", "dan", "qty", "3")),
	}
	want := []rimg.ConflictType{0, rimg.BeforeMismatch, rimg.BeforeMismatch, rimg.RowExists, rimg.BeforeMismatch, rimg.RowMissing, 0, 0}
	zero := true
	for i, e := range evs {
		before := a.Snapshot()
		res, err := a.Apply([]E{e})
		if err != nil || res[0].Conflict != want[i] {
			return false, false
		}
		zero = zero && (res[0].Applied || reflect.DeepEqual(a.Snapshot(), before))
	}
	fin := map[int64]R{1: row("name", "ann", "qty", "4"), 2: row("name", "bob", "qty", "5"), 3: row("name", "cid"), 4: row("name", "dan", "qty", "3")}
	wantC := []rimg.Conflict{{Seq: 2, PK: 2, Type: rimg.BeforeMismatch}, {Seq: 3, PK: 3, Type: rimg.BeforeMismatch}, {Seq: 4, PK: 2, Type: rimg.RowExists}, {Seq: 5, PK: 1, Type: rimg.BeforeMismatch}, {Seq: 6, PK: 4, Type: rimg.RowMissing}}
	return reflect.DeepEqual(a.Snapshot(), fin) && reflect.DeepEqual(a.Conflicts(), wantC), zero
}
func blindOK() bool {
	rng := rand.New(rand.NewPCG(1, 2))
	a, ref := api.New(map[int64]R{0: row("c", "0")}, 64), map[int64]R{0: row("c", "0")}
	seq := int64(1)
	for range 50 {
		evs := make([]E, 1+rng.IntN(4))
		for i := range evs {
			rw := func() R { return row("c", string(rune('a'+rng.IntN(4)))) }
			evs[i] = E{Seq: seq, Kind: rimg.Kind(1 + rng.IntN(3)), PK: int64(rng.IntN(8)), Before: rw(), After: rw()}
			if evs[i].Kind == rimg.Insert {
				evs[i].Before = nil
			}
			if evs[i].Kind == rimg.Delete {
				evs[i].After = nil
			}
			seq++
		}
		res, err := a.Apply(evs)
		if err != nil {
			return false
		}
		for j, e := range evs { // naive blind reference advances on applied only
			if res[j].Applied && e.Kind == rimg.Delete {
				delete(ref, e.PK)
			}
			if res[j].Applied && e.Kind != rimg.Delete {
				ref[e.PK] = e.After
			}
		}
	}
	return reflect.DeepEqual(a.Snapshot(), ref)
}
func rejectOK() bool {
	a := api.New(map[int64]R{}, 10)
	if _, e := a.Apply([]E{ins(1, 1, row("x", "1"))}); e != nil {
		return false
	}
	s, n, q := a.Snapshot(), len(a.Conflicts()), a.LastSeq()
	if _, e := a.Apply([]E{{Seq: 2, Kind: rimg.Insert, After: row("", "z")}}); e != api.ErrInvalidEvent {
		return false
	}
	if _, e := a.Apply([]E{ins(9, 2, row("x", "2"))}); e != api.ErrSeqGap {
		return false
	}
	_, over := api.New(map[int64]R{1: row("x", "1")}, 1).Apply([]E{ins(1, 2, row("x", "2"))})
	unchanged := reflect.DeepEqual(a.Snapshot(), s) && len(a.Conflicts()) == n && a.LastSeq() == q
	_, err := a.Apply([]E{ins(2, 2, row("x", "2"))}) // still usable
	return over == api.ErrTooManyRows && unchanged && err == nil
}

func concurOK() bool {
	const B, K = 40, 5
	a := api.New(map[int64]R{}, B*K)
	var bad, stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				r, c, s := a.State()
				if s%K != 0 || int64(len(r)) != s || len(c) != 0 {
					bad.Store(true)
				}
			}
		}()
	}
	go func() {
		for b := 0; b < B; b++ {
			evs := make([]E, K)
			for k := range K {
				n := int64(b*K + k + 1)
				evs[k] = ins(n, n, row("v", "x"))
			}
			if _, e := a.Apply(evs); e != nil {
				bad.Store(true)
			}
		}
		stop.Store(true)
	}()
	wg.Wait()
	return !bad.Load()
}
func main() {
	fin, zero := eight()
	f := ok("八事件每步判定: 已应用 前像不符 前像不符 行已存在 前像不符 行不存在 已应用 已应用", fin) + ok("缺列与空串不相等", !rimg.RowEqual(row("a", "1", "qty", ""), row("a", "1"))) +
		ok("随机序列与盲应用参照一致", blindOK()) + ok("冲突零副作用(三类冲突)", zero) +
		ok("三类错误可判定且被拒不留痕", rejectOK()) + ok("大m下读取行数不随m增长", rapply.ReadBoundOK()) +
		ok("并发读只见批边界状态", concurOK()) + ok("SelfCheck四不变量", api.New(map[int64]R{1: row("a", "1")}, 8).SelfCheck())
	if f > 0 {
		panic("FAIL")
	}
}
