package chain

import (
	"context"
	"errors"
	"testing"
)

// 四类对外结论必须可区分：
// StatusPreFailed / StatusPostFailed / StatusNonCriticalFailed / StatusSelfTriggerRejected。
func TestFourStatusClassesAreDistinctAndExposed(t *testing.T) {
	if StatusPreFailed == StatusPostFailed ||
		StatusPreFailed == StatusNonCriticalFailed ||
		StatusPreFailed == StatusSelfTriggerRejected ||
		StatusPostFailed == StatusNonCriticalFailed ||
		StatusPostFailed == StatusSelfTriggerRejected ||
		StatusNonCriticalFailed == StatusSelfTriggerRejected {
		t.Fatal("four exposed status classes must be distinct")
	}

	r := buildTestRegistry()

	// 1) 内层前置未通过（关键）。
	mustReg(r, &Action{
		Name:   "wrapPre",
		Pre:    func(ExecContext) (bool, string) { return true, "ok" },
		Effect: func(ExecContext) ([]WriteOp, map[string]any, string, error) { return nil, nil, "", nil },
		Post:   func(ExecContext, map[string]any) (bool, string) { return true, "ok" },
		Children: []ChildSpec{{Name: "failPre", Critical: true,
			Bind: func(Params, func(string) (any, bool)) Params { return Params{} }}},
	})
	// 2) 内层后置未通过（关键）。
	mustReg(r, &Action{
		Name:   "wrapPost",
		Pre:    func(ExecContext) (bool, string) { return true, "ok" },
		Effect: func(ExecContext) ([]WriteOp, map[string]any, string, error) { return nil, nil, "", nil },
		Post:   func(ExecContext, map[string]any) (bool, string) { return true, "ok" },
		Children: []ChildSpec{{Name: "failPost", Critical: true,
			Bind: func(p Params, _ func(string) (any, bool)) Params {
				return Params{"obj": "o"}
			}}},
	})
	// 3) 非关键失败但继续。
	mustReg(r, &Action{
		Name: "wrapNonCrit",
		Pre:  func(ExecContext) (bool, string) { return true, "ok" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return []WriteOp{{ObjectID: "o", Upsert: map[string]any{"kept": true}}}, nil, "", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "ok" },
		Children: []ChildSpec{{Name: "failPre", Critical: false,
			Bind: func(Params, func(string) (any, bool)) Params { return Params{} }}},
	})

	cases := []struct {
		entry    string
		in       Params
		want     Status
		topAbort bool
	}{
		{"wrapPre", nil, StatusPreFailed, true},
		{"wrapPost", Params{"obj": "o"}, StatusPostFailed, true},
		{"wrapNonCrit", nil, StatusNonCriticalFailed, false},
		{"loopSame", Params{"x": 1}, StatusSelfTriggerRejected, true},
	}
	for _, tc := range cases {
		eng := NewEngine(r, NewObjectStore(), nil)
		res, err := eng.Run(context.Background(), "c", tc.entry, tc.in)
		var class Status = -1
		for _, f := range res.Record.Frames {
			if f.Status == tc.want {
				class = f.Status
			}
		}
		if class != tc.want {
			t.Fatalf("%s: missing class %v in frames %v (err=%v)",
				tc.entry, tc.want, statuses(res.Record), err)
		}
		topOK := res.Record.Status == StatusCompleted
		if tc.topAbort && topOK {
			t.Fatalf("%s: top must not be Completed", tc.entry)
		}
		if !tc.topAbort && !topOK {
			t.Fatalf("%s: top must be Completed, got %v", tc.entry, res.Record.Status)
		}
	}
}

// 全局不变量破坏：链条自身前后置都通过，但组合效果违反共享不变量，提交被拒。
func TestInvariantRejectsCommittingChain(t *testing.T) {
	r := buildTestRegistry()
	store := NewObjectStore()
	store.Commit([]WriteOp{{ObjectID: "counter", Upsert: map[string]any{"n": 3}}})
	eng := NewEngine(r, store, nonNegative)

	// set 直接把 counter.n 改成 -5；动作自身后置恒真，但全局不变量失败。
	res, err := eng.Run(context.Background(), "c", "set",
		Params{"obj": "counter", "k": "n", "v": -5})
	var ie *InvariantError
	if !errors.As(err, &ie) {
		t.Fatalf("want InvariantError, got %v", err)
	}
	if res.Record.Committed || res.CommitOrder != -1 {
		t.Fatalf("chain must not commit")
	}
	if got := store.Snapshot()["counter"]["n"]; got != 3 {
		t.Fatalf("state changed despite invariant rejection: %v", got)
	}
}
