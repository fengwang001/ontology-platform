// demo 逐项打印 OK/FAIL，任一 FAIL 退出码非零。
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/decode"
	"ontology/schema"
)

// replay 朴素参照：从事件版本起逐版本按名字重放 ops 到最新版本。
func replay(init []api.Column, opsPerVer [][]api.Op, ev api.Event) []string {
	type col struct{ name, def string }
	cols := make([]col, len(init))
	for i, c := range init {
		cols[i] = col{c.Name, c.Default}
	}
	idx := func(name string) int {
		return slices.IndexFunc(cols, func(c col) bool { return c.name == name })
	}
	row := map[string]string{}
	apply := func(ops []api.Op) {
		for _, op := range ops {
			switch op.Kind {
			case schema.OpAdd:
				cols = append(cols, col{op.Name, op.Default})
				row[op.Name] = op.Default
			case schema.OpDrop:
				cols = slices.Delete(cols, idx(op.Name), idx(op.Name)+1)
				delete(row, op.Name)
			case schema.OpRename:
				cols[idx(op.Name)].name = op.NewName
				row[op.NewName] = row[op.Name]
				delete(row, op.Name)
			}
		}
	}
	for v := 2; v <= ev.Version; v++ {
		apply(opsPerVer[v-2])
	}
	row = map[string]string{}
	for i, c := range cols {
		row[c.name] = ev.Values[i]
	}
	for ; ev.Version <= len(opsPerVer); ev.Version++ {
		apply(opsPerVer[ev.Version-1])
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = row[c.name]
	}
	return out
}

func main() {
	fail := false
	ok := func(name string, cond bool) {
		s := "OK   "
		if !cond {
			s, fail = "FAIL ", true
		}
		fmt.Println(s + name)
	}
	init := []api.Column{{Name: "id"}, {Name: "name"}, {Name: "age"}}
	ops := [][]api.Op{
		{api.Rename("name", "full_name"), api.Add("email", "none")},
		{api.Drop("age"), api.Add("age", "unknown")},
	}
	h, _ := schema.New(init, 10)
	for _, o := range ops {
		h.Evolve(o)
	}
	ids := func(n int) (out []int) {
		_, v, _ := h.Snapshot(n)
		for _, c := range v.Cols {
			out = append(out, c.ID)
		}
		return out
	}
	ok("三版本列ID", slices.Equal(ids(1), []int{1, 2, 3}) &&
		slices.Equal(ids(2), []int{1, 2, 3, 4}) && slices.Equal(ids(3), []int{1, 2, 4, 5}))
	a, _ := api.New(init, 10)
	for _, o := range ops {
		a.Evolve(o)
	}
	evs := []api.Event{
		{Version: 1, Values: []string{"1", "Ann", "30"}},
		{Version: 2, Values: []string{"2", "Bob", "41", "b@x"}},
		{Version: 3, Values: []string{"3", "Cy", "c@x", "25"}},
		{Version: 2, Values: []string{"4", "Di", "19", ""}},
	}
	want := [][]string{{"1", "Ann", "none", "unknown"}, {"2", "Bob", "b@x", "unknown"},
		{"3", "Cy", "c@x", "25"}, {"4", "Di", "", "unknown"}}
	got, err := a.DecodeBatch(evs)
	ok("E1-E4解码", err == nil && slices.EqualFunc(got, want, slices.Equal))
	ok("Drop后Add同名列得新ID", a.ReadSchema()[3].ID == 5)
	ok("空串原样保留", got[3][2] == "")

	r := rand.New(rand.NewPCG(3, 4))
	a2, _ := api.New(init, 400)
	var ops2 [][]api.Op
	prev, good := "age", true
	for i := 0; i < 300 && good; i++ {
		o := []api.Op{api.Drop(prev), api.Add(fmt.Sprintf("x%d", i), "d")}
		if r.IntN(3) == 0 {
			o = append(o, api.Rename("id", "id2"), api.Rename("id2", "id"))
		}
		if _, err = a2.Evolve(o); err == nil {
			ops2 = append(ops2, o)
			prev = fmt.Sprintf("x%d", i)
		}
	}
	for i := 0; i < 50 && good; i++ {
		ev := api.Event{Version: 1 + r.IntN(len(ops2)+1), Values: []string{"a", "b", "c"}}
		g, e := a2.Decode(ev)
		good = e == nil && slices.Equal(g, replay(init, ops2, ev))
	}
	ok("随机演进与逐版本迁移参照一致", good && len(ops2) == 300)

	_, e1 := a.Decode(api.Event{Version: 0})
	_, e2 := a.Decode(api.Event{Version: 1, Values: []string{"x"}})
	_, e3 := a.Evolve([]api.Op{api.Drop("nope")})
	a4, _ := api.New(init, 1)
	_, e4 := a4.Evolve([]api.Op{api.Add("y", "")})
	ok("四类可判定错误互不相同", errors.Is(e1, api.ErrVersionNotFound) &&
		errors.Is(e2, api.ErrValueCount) && errors.Is(e3, api.ErrInvalidOp) &&
		errors.Is(e4, api.ErrTooManyVersions) && e1 != e2 && e2 != e3 && e3 != e4)

	before := a.ReadSchema()
	a.Evolve([]api.Op{api.Add("tmp", ""), api.Drop("nope")})
	n, e5 := a.Evolve([]api.Op{api.Add("z", "z")})
	ok("被拒后状态(含ID计数器)不变", e5 == nil && n == 4 &&
		a.ReadSchema()[4].ID == 6 && slices.Equal(before, a.ReadSchema()[:4]))

	h2, _ := schema.New(init, 20000)
	prev = "age"
	for i := 0; i < 10000; i++ {
		p := fmt.Sprintf("p%d", i)
		h2.Evolve([]schema.Op{schema.Drop(prev), schema.Add(p, "d")})
		prev = p
	}
	dec := decode.New(h2)
	_, err = dec.Decode(decode.Event{Version: 1, Values: []string{"1", "Ann", "30"}})
	ok("大m检查个数不随m增长", err == nil && h2.Latest().N == 10001 &&
		dec.CheckedWithin(int64(2*len(h2.Latest().Cols))))

	rows, _ := a.DecodeBatch(evs)
	var wg sync.WaitGroup
	start := make(chan struct{})
	res := make([][]string, 4*len(evs))
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i, ev := range evs {
				res[g*len(evs)+i], _ = a.Decode(ev)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	ok("并发解码结果一致", slices.EqualFunc(res, slices.Repeat(rows, 4), slices.Equal))
	ok("SelfCheck", api.SelfCheck() == nil)
	if fail {
		os.Exit(1)
	}
}
