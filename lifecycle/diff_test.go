package lifecycle_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/internal/naive"
	"ontology/lifecycle"
)

// world 是对拍场景的中立描述：若干 order/invoice 实例 + 链接，
// 同一组“初始状态 + 操作序列”会分别喂给生产引擎和朴素参考模型。
type world struct {
	orders   []string
	invoices []string
	links    [][3]string // type, from, to
	attrs    map[string]map[string]lifecycle.AttrValue
	states   map[string]string
	types    map[string]string
}

func buildProduction(w *world, log lifecycle.Logger) (*lifecycle.Store, *lifecycle.Engine) {
	st := lifecycle.NewStore()
	for _, id := range w.orders {
		st.AddInstance(&lifecycle.Instance{
			ID: id, Type: "order", State: w.states[id],
			Attrs: cloneAttrs(w.attrs[id]),
		})
	}
	for _, id := range w.invoices {
		st.AddInstance(&lifecycle.Instance{
			ID: id, Type: "invoice", State: w.states[id],
			Attrs: cloneAttrs(w.attrs[id]),
		})
	}
	eng := lifecycle.NewEngine(diffSchema(), st, log)
	for _, l := range w.links {
		st.AddLinkForSeed(l[0], l[1], l[2])
	}
	return st, eng
}

func buildNaive(w *world) *naive.Engine {
	eng := naive.NewEngine(diffNaiveSpec())
	for _, id := range w.orders {
		eng.AddInstance(&naive.Instance{
			ID: id, Type: "order", State: w.states[id],
			Attrs: cloneNaiveAttrs(w.attrs[id]),
		})
	}
	for _, id := range w.invoices {
		eng.AddInstance(&naive.Instance{
			ID: id, Type: "invoice", State: w.states[id],
			Attrs: cloneNaiveAttrs(w.attrs[id]),
		})
	}
	for _, l := range w.links {
		eng.SeedLink(l[0], l[1], l[2])
	}
	return eng
}

func cloneAttrs(a map[string]lifecycle.AttrValue) map[string]lifecycle.AttrValue {
	c := map[string]lifecycle.AttrValue{}
	for k, v := range a {
		c[k] = v
	}
	return c
}

func cloneNaiveAttrs(a map[string]lifecycle.AttrValue) map[string]naive.Value {
	c := map[string]naive.Value{}
	for k, v := range a {
		c[k] = v
	}
	return c
}

type observed struct {
	committed bool
	code      int
	state     map[string]string
	attrs     map[string]map[string]any
	links     []string
	clocks    map[string]int64
}

func observeProduction(st *lifecycle.Store, r *lifecycle.BatchResult, w *world) observed {
	o := observed{
		committed: r.Committed,
		state:     map[string]string{},
		attrs:     map[string]map[string]any{},
		clocks:    map[string]int64{},
	}
	for _, oc := range r.Outcomes {
		if oc.Err != nil {
			o.code = int(oc.Err.Code)
		}
	}
	for _, id := range append(append([]string{}, w.orders...), w.invoices...) {
		in := st.GetInstance(id)
		o.state[id] = in.State
		a := map[string]any{}
		for k, v := range in.Attrs {
			a[k] = v
		}
		o.attrs[id] = a
		o.clocks[id] = in.Clock
	}
	for _, from := range append(append([]string{}, w.orders...), w.invoices...) {
		for _, lt := range []string{"tag", "has_invoice", "next"} {
			for _, to := range st.LinksOf(from, lt) {
				o.links = append(o.links, lt+":"+from+"->"+to)
			}
		}
	}
	sort.Strings(o.links)
	return o
}

func observeNaive(eng *naive.Engine, r *naive.BatchResult, w *world) observed {
	o := observed{
		committed: r.Committed,
		state:     map[string]string{},
		attrs:     map[string]map[string]any{},
		clocks:    map[string]int64{},
	}
	for _, op := range r.Ops {
		if op.Err != nil {
			o.code = int(op.Err.Code)
		}
	}
	for _, id := range append(append([]string{}, w.orders...), w.invoices...) {
		st, _ := eng.State(id)
		o.state[id] = st
		a := map[string]any{}
		// 属性通过触发已知 key 集合读取。
		for _, k := range []string{"paid", "ok"} {
			if v, ok := eng.Attr(id, k); ok {
				a[k] = v
			}
		}
		o.attrs[id] = a
		o.clocks[id] = eng.Clock(id)
	}
	for _, from := range append(append([]string{}, w.orders...), w.invoices...) {
		for _, lt := range []string{"tag", "has_invoice", "next"} {
			for _, to := range eng.PeersOf(from, lt) {
				o.links = append(o.links, lt+":"+from+"->"+to)
			}
		}
	}
	sort.Strings(o.links)
	return o
}

func compareObserved(t *testing.T, step int, p, n observed) {
	t.Helper()
	if p.committed != n.committed {
		t.Fatalf("step %d 提交性不一致: prod=%v naive=%v", step, p.committed, n.committed)
	}
	if !p.committed && p.code != n.code {
		t.Fatalf("step %d 拒绝码不一致: prod=%d naive=%d", step, p.code, n.code)
	}
	if p.committed {
		for id := range p.state {
			if p.state[id] != n.state[id] {
				t.Fatalf("step %d 状态不一致 id=%s prod=%s naive=%s",
					step, id, p.state[id], n.state[id])
			}
			if fmt.Sprint(p.attrs[id]) != fmt.Sprint(n.attrs[id]) {
				t.Fatalf("step %d 属性不一致 id=%s prod=%v naive=%v",
					step, id, p.attrs[id], n.attrs[id])
			}
		}
		if fmt.Sprint(p.links) != fmt.Sprint(n.links) {
			t.Fatalf("step %d 链接不一致:\nprod=%v\nnaive=%v",
				step, p.links, n.links)
		}
	}
}

// TestDifferentialRandomized 用随机生成的初始结构与操作序列对拍两套实现。
func TestDifferentialRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	var buf bytes.Buffer
	logger := lifecycle.NewTextLogger(&buf)

	for iter := 0; iter < 120; iter++ {
		w := randomWorld(rng)
		st, eng := buildProduction(w, logger)
		neng := buildNaive(w)

		opsP, opsN := randomOps(rng, w, 12)
		// 日志打印每次迁移的输入、判定依据与最终结果。
		_ = opsP
		for step := range opsP {
			pRes, err := eng.Batch([]lifecycle.Op{opsP[step]})
			if err != nil {
				t.Fatalf("prod batch err: %v", err)
			}
			nRes := neng.Batch([]naive.Op{opsN[step]})
			compareObserved(t, step,
				observeProduction(st, pRes, w),
				observeNaive(neng, nRes, w))
		}
	}
	if len(buf.String()) == 0 {
		t.Fatal("决策日志应至少包含迁移记录")
	}
	t.Logf("决策日志样例（前 800 字节）:\n%s", truncate(buf.String(), 800))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestConcurrentSerialEquivalence 并发把互不冲突的操作打到生产引擎，
// 最终状态必须与朴素模型按任意随机串行排列执行的结果一致。
func TestConcurrentSerialEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for iter := 0; iter < 30; iter++ {
		w := randomWorld(rng)
		st, _ := buildProduction(w, lifecycle.DiscardLogger{})
		eng := lifecycle.NewEngine(diffSchema(), st, lifecycle.DiscardLogger{})

		all := append(append([]string{}, w.orders...), w.invoices...)
		var ops []lifecycle.Op
		var nops []naive.Op
		for _, id := range all {
			if w.states[id] == "closed" || w.states[id] == "void" {
				continue
			}
			key := "paid"
			if w.types[id] == "invoice" {
				key = "ok"
			}
			ops = append(ops, lifecycle.SetAttr(id, key, true))
			nops = append(nops, naive.Op{
				Kind: naive.OpSetAttr, InstanceID: id, Attr: key, Value: true,
			})
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, op := range ops {
			wg.Add(1)
			go func(op lifecycle.Op) {
				defer wg.Done()
				<-start
				eng.Batch([]lifecycle.Op{op})
			}(op)
		}
		close(start)
		wg.Wait()

		neng := buildNaive(w)
		perm := rng.Perm(len(nops))
		for _, idx := range perm {
			if r := neng.Batch([]naive.Op{nops[idx]}); !r.Committed {
				t.Fatalf("串行序中不应有拒绝: %+v", nops[idx])
			}
		}

		pObs := observeProduction(st, &lifecycle.BatchResult{Committed: true}, w)
		nObs := observeNaive(neng, &naive.BatchResult{Committed: true}, w)
		compareObserved(t, iter, pObs, nObs)
	}
}
