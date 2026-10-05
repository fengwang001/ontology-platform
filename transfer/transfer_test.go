package transfer_test

import (
	"errors"
	"testing"

	"ontology/transfer"
)

// scenario 是一组参数下的一段操作序列。
type scenario struct {
	name         string
	cd, r, u, wt int64
	steps        []step
}

func TestScenarios(t *testing.T) {
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			sys, err := transfer.New(sc.cd, sc.r, sc.u, sc.wt)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			runSteps(t, sys, sc.steps)
		})
	}
}

// step 是表驱动场景中的一步操作；want 为期望的错误（nil 表示成功）。
type step struct {
	op   string // newshard/create/request/complete/cancel/rename/blockers/inspect/load
	now  int64
	sid  int64
	cap  int
	char string
	name string
	dst  int64
	num  int
	view transfer.CharView // op == "inspect" 时的期望快照
	want error
}

func runSteps(t *testing.T, sys *transfer.System, steps []step) {
	t.Helper()
	for i, st := range steps {
		var err error
		switch st.op {
		case "newshard":
			err = sys.NewShard(st.sid, st.cap)
		case "create":
			err = sys.Create(st.now, st.sid, st.char, st.name)
		case "request":
			err = sys.Request(st.now, st.char, st.dst)
		case "complete":
			err = sys.Complete(st.now, st.char)
		case "cancel":
			err = sys.Cancel(st.now, st.char)
		case "rename":
			err = sys.Rename(st.now, st.char, st.name)
		case "blockers":
			err = sys.SetBlockers(st.char, st.num)
		case "inspect":
			view, ok := sys.Inspect(st.char)
			if !ok {
				t.Errorf("step %d: inspect %s: char not found", i, st.char)
			} else if view != st.view {
				t.Errorf("step %d: inspect %s = %+v, want %+v", i, st.char, view, st.view)
			}
			continue
		case "load":
			if got := sys.ShardLoad(st.sid); got != st.num {
				t.Errorf("step %d: load(%d) = %d, want %d", i, st.sid, got, st.num)
			}
			continue
		default:
			t.Fatalf("step %d: unknown op %q", i, st.op)
		}
		if !errors.Is(err, st.want) {
			t.Errorf("step %d: %s %+v: got err %v, want %v", i, st.op, st, err, st.want)
		}
	}
}
