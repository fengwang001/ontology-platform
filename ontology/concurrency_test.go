package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type serialOp struct {
	op  string // "add" 或 "mul"
	val int
}

// naiveSerialReplay 是独立的朴素串行实现：不经过引擎，直接在普通变量上
// 按给定全序逐条应用操作，作为并发执行结果的对照基准。
func naiveSerialReplay(start int, ops []serialOp) int {
	n := start
	for _, o := range ops {
		switch o.op {
		case "add":
			n += o.val
		case "mul":
			n *= o.val
		}
	}
	return n
}

// 并发触发的多条独立链条在共享对象上交织：最终状态必须等价于
// 把全部链条按某个全序（此处即提交序号序）串行执行的结果。
func TestConcurrentChainsSerializable(t *testing.T) {
	store := NewStore()
	store.Seed(Object{ID: "counter", Type: "counter", Props: map[string]any{"n": 0}})
	store.RegisterInvariant("counter", func(obj Object) error {
		if obj.Props["n"].(int) < 0 {
			return errors.New("counter must stay non-negative")
		}
		return nil
	})

	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "log",
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "log-" + ctx.Arg("id").(string), Type: "log", Props: map[string]any{}})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&Action{
		Name:  "apply",
		Calls: []CallSpec{{Action: "log", Critical: false}},
		Run: func(ctx *Context) error {
			obj, _ := ctx.Get("counter")
			n := obj.Props["n"].(int)
			switch ctx.Arg("op").(string) {
			case "add":
				n += ctx.Arg("val").(int)
			case "mul":
				n *= ctx.Arg("val").(int)
			}
			ctx.Put(Object{ID: "counter", Type: "counter", Props: map[string]any{"n": n}})
			ctx.Invoke("log", Args{"id": ctx.Arg("id").(string)}, false)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	eng := mustEngine(t, reg, store)

	const chains = 48
	ops := make([]serialOp, chains)
	for i := range ops {
		if i%3 == 0 {
			ops[i] = serialOp{op: "mul", val: 2}
		} else {
			ops[i] = serialOp{op: "add", val: i + 1}
		}
	}

	results := make([]*ChainResult, chains)
	var wg sync.WaitGroup
	for i := 0; i < chains; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = eng.Execute("apply", Args{
				"id":  fmt.Sprintf("chain-%d", i),
				"op":  ops[i].op,
				"val": ops[i].val,
			})
		}(i)
	}
	wg.Wait()

	byChainID := map[uint64]serialOp{}
	for i, res := range results {
		if !res.Committed {
			t.Fatalf("chain %d must eventually commit: %+v", i, res)
		}
		if res.Trace.Conclusion() == "" || !res.Trace.Committed() {
			t.Fatalf("chain %d trace must record the committed conclusion", i)
		}
		byChainID[res.ChainID] = ops[i]
	}

	// 按引擎给出的提交全序，用朴素串行实现重放。
	log := eng.CommitLog()
	if len(log) != chains {
		t.Fatalf("commit log must contain every chain, got %d", len(log))
	}
	ordered := make([]serialOp, 0, chains)
	for i, rec := range log {
		if rec.Seq != uint64(i+1) {
			t.Fatalf("commit seq must be a dense total order, got %+v", log)
		}
		ordered = append(ordered, byChainID[rec.ChainID])
	}
	want := naiveSerialReplay(0, ordered)

	got := mustGet(t, store, "counter").Props["n"].(int)
	if got != want {
		t.Fatalf("final state %d does not match serial replay %d", got, want)
	}
	for i := 0; i < chains; i++ {
		if _, ok := store.Get(fmt.Sprintf("log-chain-%d", i)); !ok {
			t.Fatalf("nested non-critical write of chain %d must be committed", i)
		}
	}
}

// 提交时违反对象不变量的链条被整体拒绝，存储保持不变。
func TestInvariantViolationAbortsChain(t *testing.T) {
	store := NewStore()
	store.Seed(Object{ID: "counter", Type: "counter", Props: map[string]any{"n": 5}})
	store.RegisterInvariant("counter", func(obj Object) error {
		if obj.Props["n"].(int) < 0 {
			return errors.New("counter must stay non-negative")
		}
		return nil
	})

	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "go-negative",
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "counter", Type: "counter", Props: map[string]any{"n": -1}})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	eng := mustEngine(t, reg, store)

	res := eng.Execute("go-negative", Args{})
	if !res.Aborted {
		t.Fatalf("invariant-violating chain must be aborted, got %+v", res)
	}
	if got := mustGet(t, store, "counter").Props["n"]; got != 5 {
		t.Fatalf("store must stay untouched, got %v", got)
	}
}
