package ontology

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func mustEngine(t *testing.T, reg *Registry, store *Store) *Engine {
	t.Helper()
	eng, err := NewEngine(reg, store)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng
}

func findEvents(trace *Trace, outcome Outcome) []Event {
	var out []Event
	for _, e := range trace.Events() {
		if e.Outcome == outcome {
			out = append(out, e)
		}
	}
	return out
}

func mustGet(t *testing.T, store *Store, id string) Object {
	t.Helper()
	obj, ok := store.Get(id)
	if !ok {
		t.Fatalf("object %q not found in store", id)
	}
	return obj
}

// 关键调用的后置条件失败：外层与内层已计算的写入计划一并整体放弃。
func TestCriticalFailureAbortsWholeChain(t *testing.T) {
	store := NewStore()
	store.Seed(Object{ID: "acc", Type: "account", Props: map[string]any{"balance": 100}})

	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "audit",
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "audit-log", Type: "log", Props: map[string]any{"msg": "done"}})
			return nil
		},
		Postconditions: []Condition{{
			Name: "impossible",
			Check: func(v *View) error {
				return errors.New("audit backend rejected the record")
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&Action{
		Name:  "transfer",
		Calls: []CallSpec{{Action: "audit", Critical: true}},
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "acc", Type: "account", Props: map[string]any{"balance": 90}})
			res := ctx.Invoke("audit", Args{"kind": "transfer"}, true)
			if !res.OK() {
				return fmt.Errorf("unexpected: critical call returned non-OK without abort")
			}
			ctx.Put(Object{ID: "marker", Type: "marker", Props: map[string]any{}})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	eng := mustEngine(t, reg, store)
	res := eng.Execute("transfer", Args{"amount": 10})

	if !res.Aborted || res.Committed {
		t.Fatalf("expected aborted chain, got %+v", res)
	}
	if got := mustGet(t, store, "acc").Props["balance"]; got != 100 {
		t.Fatalf("outer write plan must be discarded, balance=%v", got)
	}
	if _, ok := store.Get("audit-log"); ok {
		t.Fatal("inner write plan must be discarded together with the outer one")
	}
	if _, ok := store.Get("marker"); ok {
		t.Fatal("writes after the failed critical call must not exist")
	}
	events := findEvents(res.Trace, OutcomePostconditionFailedCritical)
	if len(events) != 1 {
		t.Fatalf("expected exactly one postcondition-failed-critical event, got %d", len(events))
	}
	if events[0].Path != "transfer/audit" || events[0].Depth != 1 {
		t.Fatalf("unexpected event path/depth: %+v", events[0])
	}
	if !strings.Contains(res.Trace.Conclusion(), "aborted") {
		t.Fatalf("trace conclusion should record abort, got %q", res.Trace.Conclusion())
	}
}

// 非关键调用失败：内层写入计划被丢弃，失败被记录，外层继续并提交自己的写入。
func TestNonCriticalFailureContinues(t *testing.T) {
	store := NewStore()
	store.Seed(Object{ID: "acc", Type: "account", Props: map[string]any{"balance": 100}})

	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "notify",
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "note", Type: "note", Props: map[string]any{"text": "hi"}})
			return nil
		},
		Postconditions: []Condition{{
			Name:  "always-fails",
			Check: func(v *View) error { return errors.New("notify channel down") },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var invokeResult *InvokeResult
	if err := reg.Register(&Action{
		Name:  "transfer",
		Calls: []CallSpec{{Action: "notify", Critical: false}},
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "acc", Type: "account", Props: map[string]any{"balance": 90}})
			invokeResult = ctx.Invoke("notify", Args{"to": "ops"}, false)
			ctx.Put(Object{ID: "after", Type: "marker", Props: map[string]any{}})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	eng := mustEngine(t, reg, store)
	res := eng.Execute("transfer", Args{})

	if !res.Committed {
		t.Fatalf("non-critical failure must not abort the chain: %+v", res)
	}
	if invokeResult.Outcome != OutcomeNonCriticalFailed || invokeResult.OK() {
		t.Fatalf("invoke result must expose the failure, got %+v", invokeResult)
	}
	if got := mustGet(t, store, "acc").Props["balance"]; got != 90 {
		t.Fatalf("outer writes must be committed, balance=%v", got)
	}
	if _, ok := store.Get("after"); !ok {
		t.Fatal("outer flow must continue after a non-critical failure")
	}
	if _, ok := store.Get("note"); ok {
		t.Fatal("failed inner write plan must be discarded")
	}
	if len(findEvents(res.Trace, OutcomeNonCriticalFailed)) != 1 {
		t.Fatal("expected one non-critical-failed event in trace")
	}
}

// 内层前置条件独立重新评估，且能看到外层尚未提交的写入计划。
func TestInnerPreconditionSeesOuterPendingWrites(t *testing.T) {
	reg := NewRegistry()
	flagOn := Condition{
		Name: "flag-on",
		Check: func(v *View) error {
			obj, ok := v.Get("flag")
			if !ok {
				return errors.New("flag missing")
			}
			if obj.Props["on"] != true {
				return errors.New("flag off")
			}
			return nil
		},
	}
	if err := reg.Register(&Action{
		Name:          "inner",
		Preconditions: []Condition{flagOn},
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "derived", Type: "doc", Props: map[string]any{}})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&Action{
		Name:  "outer",
		Calls: []CallSpec{{Action: "inner", Critical: true}},
		Run: func(ctx *Context) error {
			// 写入尚未提交，但内层前置条件必须能看到它。
			ctx.Put(Object{ID: "flag", Type: "flag", Props: map[string]any{"on": true}})
			ctx.Invoke("inner", Args{}, true)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("nested sees pending writes", func(t *testing.T) {
		store := NewStore()
		eng := mustEngine(t, reg, store)
		res := eng.Execute("outer", Args{})
		if !res.Committed {
			t.Fatalf("inner precondition should see outer pending write plan: %+v", res)
		}
		if _, ok := store.Get("derived"); !ok {
			t.Fatal("inner action should have run")
		}
		// 轨迹中内层前置求值依据必须包含外层的未提交写入键。
		var innerEvent *Event
		for _, e := range res.Trace.Events() {
			if e.Action == "inner" {
				ev := e
				innerEvent = &ev
			}
		}
		if innerEvent == nil {
			t.Fatal("missing inner event in trace")
		}
		found := false
		for _, k := range innerEvent.PreBasis.PendingWrites {
			if k == "flag" {
				found = true
			}
		}
		if !found {
			t.Fatalf("inner pre-basis must list outer pending write 'flag', got %v",
				innerEvent.PreBasis.PendingWrites)
		}
	})

	t.Run("root sees persisted state only", func(t *testing.T) {
		store := NewStore()
		eng := mustEngine(t, reg, store)
		res := eng.Execute("inner", Args{})
		if !res.Aborted {
			t.Fatalf("root precondition must only see persisted state: %+v", res)
		}
		events := findEvents(res.Trace, OutcomePreconditionFailed)
		if len(events) != 1 {
			t.Fatalf("expected one precondition-failed event, got %d", len(events))
		}
	})
}

// 内层前置条件必须独立重新评估：外层前置通过不能豁免内层前置。
func TestInnerPreconditionReevaluatedIndependently(t *testing.T) {
	store := NewStore()
	store.Seed(Object{ID: "acc", Type: "account", Props: map[string]any{"balance": 100}})

	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "risky-inner",
		Preconditions: []Condition{{
			Name: "needs-1000",
			Check: func(v *View) error {
				obj, _ := v.Get("acc")
				if obj.Props["balance"].(int) < 1000 {
					return errors.New("balance too low for inner action")
				}
				return nil
			},
		}},
		Run: func(ctx *Context) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&Action{
		Name: "outer",
		Preconditions: []Condition{{
			Name: "needs-nonnegative",
			Check: func(v *View) error {
				obj, _ := v.Get("acc")
				if obj.Props["balance"].(int) < 0 {
					return errors.New("negative balance")
				}
				return nil
			},
		}},
		Calls: []CallSpec{{Action: "risky-inner", Critical: true}},
		Run: func(ctx *Context) error {
			ctx.Invoke("risky-inner", Args{}, true)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	eng := mustEngine(t, reg, store)
	res := eng.Execute("outer", Args{})
	if !res.Aborted {
		t.Fatalf("inner precondition failure on a critical call must abort: %+v", res)
	}
	events := findEvents(res.Trace, OutcomePreconditionFailed)
	if len(events) != 1 || !events[0].Critical {
		t.Fatalf("expected one critical precondition-failed event, got %+v", events)
	}
	if got := mustGet(t, store, "acc").Props["balance"]; got != 100 {
		t.Fatalf("store must be untouched, balance=%v", got)
	}
}
