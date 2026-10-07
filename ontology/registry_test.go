package ontology

import (
	"strings"
	"testing"
)

// 对非关键调用输出的消费依赖必须在注册期（调用链条确定时）被拦截。
func TestRegisterRejectsConsumingNonCriticalOutput(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(&Action{
		Name:     "consumer",
		Calls:    []CallSpec{{ID: "c1", Action: "producer", Critical: false}},
		Consumes: []OutputRef{{Call: "c1", Output: "ticket"}},
		Run:      func(ctx *Context) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "non-critical") {
		t.Fatalf("expected declaration error for consuming non-critical output, got %v", err)
	}
}

func TestRegisterRejectsConsumingUndeclaredCall(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(&Action{
		Name:     "consumer",
		Consumes: []OutputRef{{Call: "ghost", Output: "x"}},
		Run:      func(ctx *Context) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("expected declaration error for undeclared call, got %v", err)
	}
}

func TestRegisterAcceptsConsumingCriticalOutput(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "producer",
		Run: func(ctx *Context) error {
			ctx.SetOutput("ticket", "t-1")
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&Action{
		Name:     "consumer",
		Calls:    []CallSpec{{ID: "c1", Action: "producer", Critical: true}},
		Consumes: []OutputRef{{Call: "c1", Output: "ticket"}},
		Run: func(ctx *Context) error {
			res := ctx.Invoke("producer", Args{}, true)
			if res.Outputs["ticket"] != "t-1" {
				t.Error("critical call outputs must be available to the consumer")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("critical output consumption must be accepted: %v", err)
	}
	eng := mustEngine(t, reg, NewStore())
	if res := eng.Execute("consumer", Args{}); !res.Committed {
		t.Fatalf("chain must commit: %+v", res)
	}
}

func TestEngineRejectsCallToUnregisteredAction(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name:  "caller",
		Calls: []CallSpec{{Action: "missing", Critical: true}},
		Run:   func(ctx *Context) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(reg, NewStore()); err == nil {
		t.Fatal("expected validation error for call to unregistered action")
	}
}

// 运行期发起与静态声明不一致的调用，视为声明错误并整体放弃。
func TestUndeclaredDynamicCallAborts(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "target",
		Run:  func(ctx *Context) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&Action{
		Name: "caller", // 未声明任何 Calls
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "w", Type: "marker", Props: map[string]any{}})
			ctx.Invoke("target", Args{}, true)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	eng := mustEngine(t, reg, store)
	res := eng.Execute("caller", Args{})
	if !res.Aborted || !strings.Contains(res.Reason, "declaration error") {
		t.Fatalf("undeclared dynamic call must abort with declaration error, got %+v", res)
	}
	if _, ok := store.Get("w"); ok {
		t.Fatal("aborted chain must not commit any writes")
	}
}
