package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/order"
	"ontology/rbuf"
)

var failed bool

func ok(cond bool, name string) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}
func ids(os []api.Out) (s string) {
	for _, o := range os {
		s += fmt.Sprintf(" %s:%d", o.ID, o.TS)
	}
	return strings.TrimPrefix(s, " ")
}

// checkedOf reads rbuf.Buffer's unexported counter without any exported API.
func checkedOf(b *rbuf.Buffer) int {
	f := reflect.ValueOf(b).Elem().FieldByName("checked")
	return int(reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int())
}
func main() {
	wm, has := order.Advance(0, false, 5, 3)
	wm, has = order.Advance(wm, has, 3, 3)
	ok(order.Less(9, 2, 9, 5) && !order.Less(9, 5, 9, 2) && order.Less(3, 9, 9, 0) &&
		order.Late(6, 6, true) && !order.Late(7, 6, true) && !order.Late(0, 0, false) &&
		has && wm == 2, "order: (TS,Seq) 全序 / 迟到判定 / 水位线只进不退")

	rb := rbuf.New(2)
	rb.Add(rbuf.Event{ID: "c", TS: 9, Seq: 2})
	rb.Add(rbuf.Event{ID: "b", TS: 3, Seq: 1})
	fullAtomic := rb.Add(rbuf.Event{ID: "a", TS: 5, Seq: 0}) == rbuf.ErrFull && rb.Len() == 2
	rel := rb.Release(6)
	ok(fullAtomic && len(rel) == 1 && rel[0].ID == "b" &&
		rb.Release(9)[0].ID == "c", "rbuf: (TS,Seq) 有序释放 / 满时拒绝不留痕")

	r, _ := api.New(3, 10)
	type ev struct {
		id string
		ts int64
	}
	evs := []ev{{"a", 5}, {"b", 3}, {"c", 9}, {"d", 5}, {"e", 6}, {"f", 9}, {"g", 4}, {"h", 12}, {"i", 12}, {"j", 16}}
	wantM := []string{"", "", "b:3 a:5", "", "", "", "", "c:9 f:9", "", "h:12 i:12"}
	wantS := []string{"", "", "", "d:5", "e:6", "", "g:4", "", "", ""}
	good := true
	for i, e := range evs {
		m, s, err := r.Push(e.id, e.ts)
		good = good && err == nil && ids(m) == wantM[i] && ids(s) == wantS[i]
	}
	good = good && ids(r.Flush()) == "j:16" &&
		ids(r.Main()) == "b:3 a:5 c:9 f:9 h:12 i:12 j:16" && ids(r.Side()) == "d:5 e:6 g:4"
	ok(good, "api: 十事件逐步主/旁路输出(第5步e迟到/第8步c,f/第10步h,i) + Flush 主输出")

	good = true
	for _, n := range []int{10, 100, 1000} {
		rng := rand.New(rand.NewSource(int64(n)))
		rr, _ := api.New(5, n+1)
		var maxTS int64
		have := false
		var refM, refS []api.Out
		for i := 0; i < n; i++ {
			id, ts := fmt.Sprintf("e%d", i), int64(rng.Intn(4*n))
			rr.Push(id, ts)
			o := api.Out{ID: id, TS: ts, Seq: int64(i)}
			if have && ts <= maxTS-5 {
				refS = append(refS, o)
			} else {
				refM = append(refM, o)
			}
			if !have || ts > maxTS {
				maxTS, have = ts, true
			}
		}
		rr.Flush()
		sort.SliceStable(refM, func(i, j int) bool { return refM[i].TS < refM[j].TS })
		gm := rr.Main()
		good = good && reflect.DeepEqual(gm, refM) && reflect.DeepEqual(rr.Side(), refS) &&
			slices.IsSortedFunc(gm, func(a, b api.Out) int { return order.Compare(a.TS, a.Seq, b.TS, b.Seq) })
	}
	sc, _ := api.New(1, 1)
	ok(good && sc.SelfCheck() == nil, "api: 随机乱序与朴素参照一致 / 主输出严格有序 / SelfCheck")

	_, e1 := api.New(-1, 1)
	r2, _ := api.New(3, 1)
	r2.Push("x", 100)
	m0, s0 := r2.Main(), r2.Side()
	_, _, e2 := r2.Push("", 1)
	_, _, e3 := r2.Push("x", 1)
	_, _, e4 := r2.Push("y", 1000)
	diff := len(map[error]bool{e1: true, e2: true, e3: true, e4: true}) == 4 && e1 != nil
	untouched := reflect.DeepEqual(r2.Main(), m0) && reflect.DeepEqual(r2.Side(), s0)
	_, _, e5 := r2.Push("z", 0) // 拒绝后仍可使用：z 是合法迟到事件，进旁路
	ok(diff && e5 == nil && untouched, "api: 四类错误可判定互不相同 / 被拒后状态不变且仍可用")

	good = true
	for _, m := range []int{100, 1000, 10000} {
		b := rbuf.New(m + 2)
		b.Add(rbuf.Event{ID: "k", TS: 5, Seq: 0})
		for i := 0; i < m; i++ {
			b.Add(rbuf.Event{ID: fmt.Sprintf("m%d", i), TS: 1 << 40, Seq: int64(i + 1)})
		}
		good = good && len(b.Release(10)) == 1 && checkedOf(b) <= 2
	}
	ok(good, "rbuf: 大 m 下释放检查个数 <= k+1，不随 m 增长")

	const G, P = 8, 50
	rc, _ := api.New(7, G*P)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Go(func() {
			for p := 0; p < P; p++ {
				rc.Push(fmt.Sprintf("g%dp%d", g, p), int64((g*P+p)*13%97))
			}
		})
	}
	wg.Wait()
	rc.Flush()
	gm := rc.Main()
	all := append(slices.Clone(gm), rc.Side()...)
	cnt := map[string]int{}
	for _, o := range all {
		cnt[o.ID]++
	}
	ok(len(all) == G*P && len(cnt) == G*P && slices.IsSortedFunc(gm, func(a, b api.Out) int {
		return order.Compare(a.TS, a.Seq, b.TS, b.Seq)
	}), "api: 并发 Push 后恰好一次且主输出有序")

	if failed {
		os.Exit(1)
	}
}
