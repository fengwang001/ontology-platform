package lifecycle

import (
	"fmt"
	"sync"
	"testing"
)

// 7) 终态实例的全面保护：不可迁移、不可改属性、不可新增链接；删除允许。
func TestTerminalProtection(t *testing.T) {
	e, s, _ := newTestEngine()
	mustRegister(t, s, orderTypes())
	mustCreate(t, s, "o", "Order")
	mustCreate(t, s, "x", "Order")
	mustCreate(t, s, "y", "Order")

	mustSetAttrs(t, e, "o", AttrOp{Key: "balance", Op: AttrSet, Value: int64(1)})
	mustLink(t, e, "o", "items", "x")
	if out := e.Execute(TransitionRequest{Instance: "o", Rule: "pay"}); !out[0].OK() {
		t.Fatalf("pay: %+v", out[0].Err)
	}
	if out := e.Execute(TransitionRequest{Instance: "o", Rule: "ship"}); !out[0].OK() {
		t.Fatalf("ship: %+v", out[0].Err)
	}
	mustLink(t, e, "o", "items", "y")
	if out := e.Execute(TransitionRequest{Instance: "o", Rule: "close"}); !out[0].OK() {
		t.Fatalf("close: %+v", out[0].Err)
	}
	before, _ := s.SnapshotInstance("o")

	if out := e.Execute(TransitionRequest{Instance: "o", Rule: "ship"}); out[0].Err.Code != ErrUndeclared {
		t.Fatalf("terminal migrate want undeclared, got %+v", out[0].Err)
	}
	if err := e.SetAttrs("o", AttrOp{Key: "balance", Op: AttrSet, Value: int64(99)}); err == nil || err.Code != ErrTerminal {
		t.Fatalf("terminal setattr want ErrTerminal, got %+v", err)
	}
	if err := e.ModifyLinks("o", LinkOp{Link: "items", Target: "y", Op: LinkAdd}); err == nil || err.Code != ErrTerminal {
		t.Fatalf("terminal link-add want ErrTerminal, got %+v", err)
	}
	if err := e.ModifyLinks("o", LinkOp{Link: "items", Target: "x", Op: LinkDel}); err != nil {
		t.Fatalf("terminal link-del should be allowed, got %+v", err)
	}
	after, _ := s.SnapshotInstance("o")
	if after.State != before.State || after.Attrs["balance"] != before.Attrs["balance"] {
		t.Fatalf("terminal observable state changed: before=%+v after=%+v", before, after)
	}
	if n := len(s.Neighbors("o", "items")); n != 1 {
		t.Fatalf("only deletion should take effect, neighbors=%v", s.Neighbors("o", "items"))
	}
}

// 8a) 并发触发同一实例两条有顺序依赖的迁移：结果必须恰好等于两种合法
// 串行顺序之一（[inc,inc2] 全成功到 c2；[inc2,inc] 前者 undeclared、
// 后者成功到 c1），不允许出现第三种状态。
func TestConcurrentSameInstanceSerializable(t *testing.T) {
	s := NewStore()
	mustRegister(t, s, &ObjectType{
		Name:    "Cnt",
		States:  []State{"c0", "c1", "c2"},
		Initial: "c0",
		Transitions: map[string]*TransitionRule{
			"inc":  {Name: "inc", From: []State{"c0"}, To: "c1"},
			"inc2": {Name: "inc2", From: []State{"c1"}, To: "c2"},
		},
	})
	e := NewEngine(s, NewMemoryLogger())
	mustCreate(t, s, "k", "Cnt")

	var wg sync.WaitGroup
	var mu sync.Mutex
	var codes []ErrorCode
	fire := func(rule string) {
		defer wg.Done()
		out := e.Execute(TransitionRequest{Instance: "k", Rule: rule})
		mu.Lock()
		defer mu.Unlock()
		if out[0].Err != nil {
			codes = append(codes, out[0].Err.Code)
		} else {
			codes = append(codes, 0)
		}
	}
	wg.Add(2)
	go fire("inc")
	go fire("inc2")
	wg.Wait()

	got := stateOf(t, s, "k")
	accepted, undeclared := 0, 0
	for _, c := range codes {
		switch c {
		case 0:
			accepted++
		case ErrUndeclared:
			undeclared++
		default:
			t.Fatalf("unexpected code %v", c)
		}
	}
	switch got {
	case "c2":
		if accepted != 2 || undeclared != 0 {
			t.Fatalf("c2 requires both accepted, got %v", codes)
		}
	case "c1":
		if accepted != 1 || undeclared != 1 {
			t.Fatalf("c1 requires 1 accept + 1 undeclared, got %v", codes)
		}
	default:
		t.Fatalf("state=%s has no serial-order explanation; results=%v", got, codes)
	}
}

// 8b) 并发触发两个无链式关系的不同实例：互不阻塞，各自生效。
func TestConcurrentDifferentInstances(t *testing.T) {
	s := NewStore()
	mustRegister(t, s, &ObjectType{
		Name:    "Cnt",
		States:  []State{"c0", "c1"},
		Initial: "c0",
		Transitions: map[string]*TransitionRule{
			"inc": {Name: "inc", From: []State{"c0"}, To: "c1"},
		},
	})
	e := NewEngine(s, NewMemoryLogger())
	n := 40
	for i := 0; i < n; i++ {
		mustCreate(t, s, InstanceID(fmt.Sprintf("k%d", i)), "Cnt")
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			out := e.Execute(TransitionRequest{Instance: InstanceID(fmt.Sprintf("k%d", i)), Rule: "inc"})
			if out[0].Err != nil {
				errCh <- out[0].Err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("independent transition failed: %v", err)
	}
	for i := 0; i < n; i++ {
		if got := stateOf(t, s, InstanceID(fmt.Sprintf("k%d", i))); got != "c1" {
			t.Fatalf("k%d=%s want c1", i, got)
		}
	}
}

// 9) 错误固定优先级：未声明优先于前置条件。
func TestErrorPriorityOrdering(t *testing.T) {
	s := NewStore()
	mustRegister(t, s, &ObjectType{
		Name:          "P",
		States:        []State{"p0", "p1", "term"},
		Initial:       "p0",
		TerminalsList: []State{"term"},
		Transitions: map[string]*TransitionRule{
			"go": {Name: "go", From: []State{"p0"}, To: "p1"},
		},
	})
	e := NewEngine(s, NewMemoryLogger())
	mustCreate(t, s, "p", "P")
	// 规则名不存在，即使前置条件/终态等问题同时“可想象”，也报未声明。
	out := e.Execute(TransitionRequest{Instance: "p", Rule: "missing"})
	if out[0].Err.Code != ErrUndeclared {
		t.Fatalf("want ErrUndeclared highest, got %+v", out[0].Err)
	}
}
