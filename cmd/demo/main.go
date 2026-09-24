// Command demo exercises the incremental LAG view end to end.
package main

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/lagord"
	"ontology/lagview"
)

var failed bool

func ok(name string, cond bool) {
	status := "OK"
	if !cond {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", status, name)
}

func render(cs []api.Change) string {
	var b strings.Builder
	for _, c := range cs {
		sign, lag := "+", "NULL"
		if c.Del {
			sign = "-"
		}
		if c.Lag != nil {
			lag = strconv.FormatInt(*c.Lag, 10)
		}
		fmt.Fprintf(&b, "%s(%d,%s)", sign, c.ID, lag)
	}
	return b.String()
}

// batch is the demo-side independent batch recompute of the LAG view.
func batch(rows map[int64]api.Op) map[int64]*int64 {
	byPart := map[string][]api.Op{}
	for _, op := range rows {
		byPart[op.Part] = append(byPart[op.Part], op)
	}
	out := map[int64]*int64{}
	for _, rs := range byPart {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Sort < rs[j].Sort || rs[i].Sort == rs[j].Sort && rs[i].ID < rs[j].ID })
		for i, op := range rs {
			var lag *int64
			if i > 0 {
				lag = &rs[i-1].Val
			}
			out[op.ID] = lag
		}
	}
	return out
}

func eqView(a, b map[int64]*int64) bool { return maps.EqualFunc(a, b, lagview.EqLag) }

func main() {
	ops := []api.Op{{ID: 1, Part: "p", Sort: 10, Val: 5}, {ID: 2, Part: "p", Sort: 30, Val: 8},
		{ID: 3, Part: "p", Sort: 20, Val: 7}, {ID: 4, Part: "q", Sort: 15, Val: 9},
		{ID: 5, Part: "p", Sort: 20, Val: 6}, {Del: true, ID: 3},
		{ID: 6, Part: "p", Sort: 5, Val: 5}, {Del: true, ID: 1}}
	want := []string{"+(1,NULL)", "+(2,5)", "+(3,5)-(2,5)+(2,7)", "+(4,NULL)",
		"+(5,7)-(2,7)+(2,6)", "-(3,5)-(5,7)+(5,5)", "+(6,NULL)-(1,NULL)+(1,5)", "-(1,5)"}
	note := []string{"", "", "", "", " 并列按ID升序", "", "", " 值没变不输出"}
	x, rows, down := api.New(100), map[int64]api.Op{}, map[int64]*int64{}
	viewOK, logOK := true, true
	for i, op := range ops {
		cs, err := x.Apply([]api.Op{op})
		for _, c := range cs {
			logOK = lagview.ApplyChange(down, c) && logOK
		}
		rows[op.ID] = op
		if op.Del {
			delete(rows, op.ID)
		}
		viewOK = viewOK && eqView(x.View(), batch(rows)) && eqView(x.View(), down)
		ok(fmt.Sprintf("step%d %s%s", i+1, render(cs), note[i]), err == nil && render(cs) == want[i])
	}
	s := &lagord.Seq{} // lagord: shuffled inserts, ties broken by ID asc
	for _, r := range []lagord.Row{{ID: 5, Sort: 20}, {ID: 3, Sort: 20}, {ID: 1, Sort: 10}, {ID: 2, Sort: 30}} {
		s.Insert(r)
	}
	ordOK := s.At(0).ID == 1 && s.At(1).ID == 3 && s.At(2).ID == 5 && s.At(3).ID == 2
	a1, a2 := api.New(100), api.New(100) // invariant 3: random insert order
	_, _ = a1.Apply(ops[:5])
	for _, i := range rand.New(rand.NewSource(1)).Perm(5) {
		_, _ = a2.Apply([]api.Op{ops[i]})
	}
	ok("lagord有序/每步视图==批量重算/日志前缀自洽/随机顺序一致/SelfCheck",
		ordOK && viewOK && logOK && eqView(a1.View(), a2.View()) && x.SelfCheck() == nil)
	y := api.New(2) // four distinguishable sentinel errors; rejection leaves no trace
	_, _ = y.Apply([]api.Op{{ID: 1, Part: "p"}, {ID: 2, Part: "p"}})
	before, errOK := y.View(), true
	for _, b := range []struct {
		ops  []api.Op
		want error
	}{
		{[]api.Op{{ID: 1, Part: "p"}}, api.ErrDupID},
		{[]api.Op{{Del: true, ID: 9}}, api.ErrNoID},
		{[]api.Op{{ID: 3}}, api.ErrBadPart},
		{[]api.Op{{ID: 3, Part: "p"}}, api.ErrTooMany},
	} {
		cs, err := y.Apply(b.ops)
		errOK = errOK && errors.Is(err, b.want) && cs == nil
	}
	big, bigRows := api.New(1<<20), map[int64]api.Op{} // large m: insert/delete in the middle
	for i := 0; i < 10000; i++ {
		op := api.Op{ID: int64(i), Part: "p", Sort: int64(i), Val: int64(i % 7)}
		_, _ = big.Apply([]api.Op{op})
		bigRows[op.ID] = op
	}
	_, _ = big.Apply([]api.Op{{ID: 10001, Part: "p", Sort: 5000, Val: 3}})
	_, _ = big.Apply([]api.Op{{Del: true, ID: 10001}})
	bigOK := eqView(big.View(), batch(bigRows))
	z := api.New(300) // concurrent readers must see identical views
	var ins []api.Op
	for i := 0; i < 300; i++ {
		ins = append(ins, api.Op{ID: int64(i), Part: "p", Sort: int64(i)})
	}
	_, _ = z.Apply(ins)
	gold := z.View()
	var wg sync.WaitGroup
	var concBad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Go(func() {
			for i := 0; i < 30; i++ {
				if !eqView(z.View(), gold) {
					concBad.Store(true)
				}
			}
		})
	}
	wg.Wait()
	ok("四类哨兵错误/拒后状态不变/大m亚线性(白盒TestLocateComparisonsSublinear)/并发只读一致",
		errOK && eqView(before, y.View()) && bigOK && !concBad.Load())
	if failed {
		os.Exit(1)
	}
}
