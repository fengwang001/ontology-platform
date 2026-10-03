package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func copyInstr(src, length int64) Instr {
	return Instr{Kind: CopyInstr, Src: src, Len: length}
}

func addInstr(data string) Instr {
	return Instr{Kind: AddInstr, Data: []byte(data)}
}

func expectedFile(old []byte, delta []Instr) []byte {
	var want bytes.Buffer
	for _, instr := range delta {
		switch instr.Kind {
		case CopyInstr:
			want.Write(old[instr.Src : instr.Src+instr.Len])
		case AddInstr:
			want.Write(instr.Data)
		}
	}
	return want.Bytes()
}

func executePlan(old []byte, plan PlanResult, m int64) []byte {
	buffer := make([]byte, maxInt64(int64(len(old)), m))
	copy(buffer, old)
	slots := make(map[int][]byte)
	for _, op := range plan.Operations {
		switch op.Kind {
		case StashOp:
			slots[op.Slot] = append([]byte(nil), buffer[op.Src:op.Src+op.Len]...)
		case CopyOp:
			copy(buffer[op.Dst:op.Dst+op.Len], bytes.Clone(buffer[op.Src:op.Src+op.Len]))
		case UnstashOp:
			copy(buffer[op.Dst:op.Dst+op.Len], slots[op.Slot])
		case AddOp:
			copy(buffer[op.Dst:op.Dst+int64(len(op.Data))], op.Data)
		}
	}
	return buffer[:m]
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func assertPlan(t *testing.T, planner *Planner, old []byte, n int64, delta []Instr) PlanResult {
	t.Helper()
	plan, err := planner.Plan(n, delta)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	want := expectedFile(old, delta)
	got := executePlan(old, plan, int64(len(want)))
	if !bytes.Equal(got, want) {
		t.Fatalf("executed plan = %q, want %q", got, want)
	}
	return plan
}

func TestSwapHalves(t *testing.T) {
	planner := NewPlanner(10, 10)
	old := []byte("0123456789")
	delta := []Instr{copyInstr(5, 5), copyInstr(0, 5)}
	plan := assertPlan(t, planner, old, 10, delta)

	wantOps := []Operation{
		{Kind: StashOp, Src: 5, Dst: 0, Len: 5, Slot: 0},
		{Kind: CopyOp, Src: 0, Dst: 5, Len: 5},
		{Kind: UnstashOp, Dst: 0, Len: 5, Slot: 0},
	}
	assertOperations(t, plan.Operations, wantOps)
	if plan.StashBytes != 5 || plan.StashedCopies != 1 || plan.Edges != 2 {
		t.Fatalf("plan stats = bytes:%d count:%d edges:%d", plan.StashBytes, plan.StashedCopies, plan.Edges)
	}
}

func TestCopyThenAddExample(t *testing.T) {
	planner := NewPlanner(10, 10)
	old := []byte("012345678")
	delta := []Instr{addInstr("ab"), copyInstr(0, 8)}
	plan := assertPlan(t, planner, old, 9, delta)

	wantOps := []Operation{
		{Kind: CopyOp, Src: 0, Dst: 2, Len: 8},
		{Kind: AddOp, Dst: 0, Data: []byte("ab")},
	}
	assertOperations(t, plan.Operations, wantOps)
	if plan.StashBytes != 0 || plan.StashedCopies != 0 || plan.Edges != 0 {
		t.Fatalf("plan stats = bytes:%d count:%d edges:%d", plan.StashBytes, plan.StashedCopies, plan.Edges)
	}
}

func TestRotateThree(t *testing.T) {
	planner := NewPlanner(9, 9)
	old := []byte("abcdefghi")
	delta := []Instr{copyInstr(3, 3), copyInstr(6, 3), copyInstr(0, 3)}
	plan := assertPlan(t, planner, old, 9, delta)

	wantOps := []Operation{
		{Kind: StashOp, Src: 3, Dst: 0, Len: 3, Slot: 0},
		{Kind: CopyOp, Src: 6, Dst: 3, Len: 3},
		{Kind: CopyOp, Src: 0, Dst: 6, Len: 3},
		{Kind: UnstashOp, Dst: 0, Len: 3, Slot: 0},
	}
	assertOperations(t, plan.Operations, wantOps)
	if plan.StashBytes != 3 || plan.StashedCopies != 1 || plan.Edges != 3 {
		t.Fatalf("plan stats = bytes:%d count:%d edges:%d", plan.StashBytes, plan.StashedCopies, plan.Edges)
	}
}

func TestSelfCopyDiscarded(t *testing.T) {
	planner := NewPlanner(10, 10)
	old := []byte("0123456789")
	delta := []Instr{copyInstr(0, 3), addInstr("x")}
	plan := assertPlan(t, planner, old, 10, delta)
	if len(plan.Operations) != 1 || plan.Operations[0].Kind != AddOp || plan.Edges != 0 {
		t.Fatalf("operations = %#v", plan.Operations)
	}
}

func TestNewFileLongerThanOld(t *testing.T) {
	planner := NewPlanner(20, 20)
	old := []byte("old")
	delta := []Instr{addInstr("prefix-"), copyInstr(0, 3), addInstr("-suffix")}
	plan := assertPlan(t, planner, old, 3, delta)
	if plan.StashedCopies != 0 || plan.Edges != 0 {
		t.Fatalf("unexpected graph: %#v", plan)
	}
}

func TestValidationAndStats(t *testing.T) {
	planner := NewPlanner(2, 1)
	if _, err := planner.Plan(-1, nil); !errors.Is(err, ErrSize) {
		t.Fatalf("n<0 error = %v", err)
	}
	if _, err := planner.Plan(3, nil); !errors.Is(err, ErrSize) {
		t.Fatalf("n>MaxSize error = %v", err)
	}
	if _, err := planner.Plan(2, []Instr{copyInstr(0, 3)}); !errors.Is(err, ErrBadDelta) {
		t.Fatalf("copy overflow error = %v", err)
	}
	if _, err := planner.Plan(2, []Instr{addInstr("")}); !errors.Is(err, ErrBadDelta) {
		t.Fatalf("empty add error = %v", err)
	}
	if _, err := planner.Plan(2, []Instr{copyInstr(0, 1), addInstr("x")}); !errors.Is(err, ErrBadDelta) {
		t.Fatalf("instruction limit error = %v", err)
	}
	sizePlanner := NewPlanner(2, 2)
	if _, err := sizePlanner.Plan(2, []Instr{copyInstr(0, 1), addInstr("xx")}); !errors.Is(err, ErrSize) {
		t.Fatalf("m>MaxSize error = %v", err)
	}
	if stats := planner.Stats(); stats != (StatsSnapshot{}) {
		t.Fatalf("rejected calls changed stats: %#v", stats)
	}
	if _, err := planner.Plan(1, []Instr{copyInstr(0, 1)}); err != nil {
		t.Fatalf("accepted plan: %v", err)
	}
	if stats := planner.Stats(); stats.AcceptedPlans != 1 || stats.TotalGraphCopies != 0 {
		t.Fatalf("stats after self-copy plan = %#v", stats)
	}
}

func assertOperations(t *testing.T, got, want []Operation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("operations len = %d, want %d; got %#v", len(got), len(want), got)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("operation %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestAddDataNotAliased(t *testing.T) {
	planner := NewPlanner(10, 10)
	data := []byte("abc")
	plan, err := planner.Plan(3, []Instr{addInstr(string(data))})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, op := range plan.Operations {
		if op.Kind == AddOp {
			op.Data[0] = 'Z'
		}
	}
	if got := fmt.Sprintf("%s", data); got != "abc" {
		t.Fatalf("input data changed: %s", got)
	}
}

func TestConcurrentPlansAndStats(t *testing.T) {
	planner := NewPlanner(10, 10)
	old := []byte("0123456789")
	delta := []Instr{copyInstr(5, 5), copyInstr(0, 5)}
	var wg sync.WaitGroup
	results := make(chan PlanResult, 32)
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plan, err := planner.Plan(10, delta)
			if err != nil {
				t.Errorf("concurrent Plan: %v", err)
				return
			}
			got := executePlan(old, plan, 10)
			if string(got) != "5678901234" {
				t.Errorf("concurrent result = %q", got)
			}
			results <- plan
		}()
	}
	wg.Wait()
	close(results)

	var first PlanResult
	count := 0
	for plan := range results {
		if count == 0 {
			first = plan
		} else if !reflect.DeepEqual(plan, first) {
			t.Fatalf("concurrent plans differ: %#v vs %#v", plan, first)
		}
		count++
	}
	stats := planner.Stats()
	if stats.AcceptedPlans != 32 || stats.TotalStashBytes != 160 || stats.TotalGraphCopies != 64 {
		t.Fatalf("stats = %#v", stats)
	}
}
