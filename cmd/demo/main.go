package main

import (
	"fmt"
	"maps"
	"math"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/fileref"
	"ontology/snapchain"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		os.Exit(1)
	}
}
func ss(v ...string) []string { return v }
func ufiles(sn []snapchain.Snapshot) []string {
	m := map[string]struct{}{}
	for _, s := range sn {
		for _, f := range s.Files {
			m[f] = struct{}{}
		}
	}
	return append([]string{}, slices.Sorted(maps.Keys(m))...) // always non-nil
}
func sub(a, b []string) []string { // sorted set difference a - b
	mb := map[string]struct{}{}
	for _, x := range b {
		mb[x] = struct{}{}
	}
	o := []string{}
	for _, x := range a {
		if _, ok := mb[x]; !ok {
			o = append(o, x)
		}
	}
	return o
}
func coherent(tab *api.Table) bool { sn, fl := tab.State(); return reflect.DeepEqual(fl, ufiles(sn)) }
func desc(snaps []snapchain.Snapshot) string {
	p := make([]string, len(snaps))
	for i, s := range snaps {
		p[i] = "S" + strconv.Itoa(s.ID) + "{" + strings.Join(s.Files, ",") + "}"
	}
	return strings.Join(p, " ")
}

func main() {
	tab := api.New()
	steps := []struct {
		exp      bool
		n        int
		ts       int64
		add, rem []string
	}{
		{false, 0, 10, ss("f1", "f2"), nil}, {false, 0, 20, ss("f3"), ss("f1")}, {false, 0, 30, ss("f4"), ss("f2")}, {false, 0, 40, ss("f5"), ss("f3")}, {true, 1, 15, nil, nil}, {false, 0, 50, ss("f6"), ss("f4")}, {true, 2, 35, nil, nil}, {true, 0, 50, nil, nil},
	}
	wantDel := [][]string{nil, nil, nil, nil, ss("f1"), nil, ss("f2", "f3"), ss("f4")}
	tr, ok8 := strings.Builder{}, true
	for i, x := range steps {
		var del []string
		if x.exp {
			del, _ = tab.Expire(x.n, x.ts)
		} else {
			_, _ = tab.Commit(x.ts, x.add, x.rem)
		}
		ok8 = ok8 && reflect.DeepEqual(del, wantDel[i])
		fmt.Fprintf(&tr, "%d:%s del%v | ", i+1, desc(tab.Snapshots()), del)
	}
	report("八步序列 "+strings.TrimSuffix(tr.String(), " | "), ok8)
	u := api.New()
	_, _ = u.Commit(10, ss("a", "b"), nil)
	_, _ = u.Commit(20, ss("c"), ss("a"))
	du, _ := u.Expire(1, 5)
	report("条件取并:旧快照S1仅满足ts>T仍保留,无删除", len(u.Snapshots()) == 2 && len(du) == 0)
	report("当前快照永不过期:第8步S5仍在", reflect.DeepEqual(tab.Snapshots()[0].Files, ss("f5", "f6")))
	rng, r := rand.New(rand.NewSource(1)), api.New()
	rok, tcur := true, int64(0)
	for it := 0; it < 200; it++ {
		before := r.Snapshots()
		if len(before) > 0 && rng.Intn(2) == 0 {
			n, T := rng.Intn(len(before)+2), rng.Int63n(tcur+2)
			del, _ := r.Expire(n, T)
			rok = rok && reflect.DeepEqual(del, sub(ufiles(before), ufiles(r.Snapshots())))
		} else {
			tcur++
			_, _ = r.Commit(tcur, ss("x"+strconv.Itoa(it)), nil)
		}
		rok = rok && coherent(r)
	}
	report("随机序列与朴素参照一致", rok)
	report("保留快照可读且无泄漏(store==并集)", rok)
	b := api.New()
	_, _ = b.Commit(10, ss("f1", "f2"), nil)
	_, e1 := b.Expire(-1, 0)
	_, e2 := b.Commit(10, ss("z"), nil)
	_, e3 := b.Commit(11, ss("f1"), nil)
	_, e4 := b.Commit(11, nil, ss("zz"))
	report("四类错误可判定且互不相同", e1 == fileref.ErrNegativeN && e2 == snapchain.ErrTSNotIncreasing && e3 == fileref.ErrNameConflict && e4 == snapchain.ErrFileNotInCurrent)
	untouched := len(b.Snapshots()) == 1 && reflect.DeepEqual(b.Files(), ss("f1", "f2"))
	_, ue := b.Commit(11, ss("g"), ss("f1"))
	report("被拒后状态不变且仍可用", untouched && ue == nil)
	bg := api.New()
	_, _ = bg.Commit(1, ss("a"), nil)
	many := make([]string, 10000)
	for i := range many {
		many[i] = "b" + strconv.Itoa(i)
	}
	_, _ = bg.Commit(2, many, ss("a"))
	bd, _ := bg.Expire(1, math.MaxInt64)
	report("大m=10000仅过期S1删a保留完好", reflect.DeepEqual(bd, ss("a")) && len(bg.Files()) == 10000)
	c, start := api.New(), make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Bool
	spawn := func(fn func()) { wg.Add(1); go func() { defer wg.Done(); <-start; fn() }() }
	spawn(func() {
		for i := 1; i <= 200; i++ {
			_, _ = c.Commit(int64(i), ss("p"+strconv.Itoa(i)), nil)
			if i%7 == 0 {
				_, _ = c.Expire(i%3, int64(i)/2)
			}
		}
	})
	for range 3 {
		spawn(func() {
			for range 2000 {
				if !coherent(c) {
					bad.Store(true)
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
	report("并发读一致", !bad.Load())
}
