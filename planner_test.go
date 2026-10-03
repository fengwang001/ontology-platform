package ontology

import (
	"bytes"
	"reflect"
	"sync"
	"testing"
)

func TestSwapHalvesExample(t *testing.T) {
	sorter := NewSorter(10, 10)
	instructions := []Instruction{
		{Copy: &Copy{Src: 5, Len: 5}},
		{Copy: &Copy{Src: 0, Len: 5}},
	}
	plan, err := sorter.Plan(10, instructions)
	if err != nil {
		t.Fatal(err)
	}

	want := PlanResult{
		Ops: []Op{
			{Kind: OpStash, Src: 5, Len: 5, Slot: 0},
			{Kind: OpCopy, Src: 0, Dst: 5, Len: 5},
			{Kind: OpUnstash, Dst: 0, Len: 5, Slot: 0},
		},
		StashBytes:       5,
		StashedCopyCount: 1,
		Edges:            2,
	}
	assertPlan(t, plan, want)
	assertExecutes(t, 10, instructions, plan, []byte("FGHIJABCDE"))
}

func TestLiteralPrefixExample(t *testing.T) {
	sorter := NewSorter(10, 10)
	instructions := []Instruction{
		{Add: &Add{Data: []byte("ab")}},
		{Copy: &Copy{Src: 0, Len: 8}},
	}
	plan, err := sorter.Plan(10, instructions)
	if err != nil {
		t.Fatal(err)
	}

	want := PlanResult{
		Ops: []Op{
			{Kind: OpCopy, Src: 0, Dst: 2, Len: 8},
			{Kind: OpAdd, Dst: 0, Data: []byte("ab")},
		},
		StashBytes:       0,
		StashedCopyCount: 0,
		Edges:            0,
	}
	assertPlan(t, plan, want)
	assertExecutes(t, 10, instructions, plan, append([]byte("ab"), oldData(8)...))
}

func TestRotateThreeExample(t *testing.T) {
	sorter := NewSorter(9, 10)
	instructions := []Instruction{
		{Copy: &Copy{Src: 3, Len: 3}},
		{Copy: &Copy{Src: 6, Len: 3}},
		{Copy: &Copy{Src: 0, Len: 3}},
	}
	plan, err := sorter.Plan(9, instructions)
	if err != nil {
		t.Fatal(err)
	}

	want := PlanResult{
		Ops: []Op{
			{Kind: OpStash, Src: 3, Len: 3, Slot: 0},
			{Kind: OpCopy, Src: 6, Dst: 3, Len: 3},
			{Kind: OpCopy, Src: 0, Dst: 6, Len: 3},
			{Kind: OpUnstash, Dst: 0, Len: 3, Slot: 0},
		},
		StashBytes:       3,
		StashedCopyCount: 1,
		Edges:            3,
	}
	assertPlan(t, plan, want)
	assertExecutes(t, 9, instructions, plan, []byte("DEFGHIABC"))
}

func TestSpecialGraphShapes(t *testing.T) {
	tests := []struct {
		name      string
		n         int64
		delta     []Instruction
		wantStash int64
	}{
		{
			name: "two rings connected by edge",
			n:    16,
			delta: []Instruction{
				{Copy: &Copy{Src: 4, Len: 4}},
				{Copy: &Copy{Src: 0, Len: 4}},
				{Copy: &Copy{Src: 12, Len: 4}},
				{Copy: &Copy{Src: 8, Len: 4}},
			},
			wantStash: 8,
		},
		{
			name: "ring downstream acyclic node",
			n:    12,
			delta: []Instruction{
				{Copy: &Copy{Src: 2, Len: 2}},
				{Copy: &Copy{Src: 6, Len: 4}},
				{Copy: &Copy{Src: 0, Len: 2}},
				{Copy: &Copy{Src: 10, Len: 2}},
			},
			wantStash: 2,
		},
		{
			name: "same copy in two cycles",
			n:    8,
			delta: []Instruction{
				{Copy: &Copy{Src: 4, Len: 4}},
				{Copy: &Copy{Src: 0, Len: 2}},
				{Copy: &Copy{Src: 0, Len: 2}},
			},
			wantStash: 4,
		},
		{
			name: "tie breaks by destination then smaller length",
			n:    7,
			delta: []Instruction{
				{Copy: &Copy{Src: 1, Len: 1}},
				{Copy: &Copy{Src: 0, Len: 1}},
				{Copy: &Copy{Src: 6, Len: 1}},
				{Copy: &Copy{Src: 5, Len: 2}},
				{Copy: &Copy{Src: 3, Len: 2}},
			},
			wantStash: 3,
		},
		{
			name: "shorter copy wins despite larger destination",
			n:    4,
			delta: []Instruction{
				{Copy: &Copy{Src: 2, Len: 2}},
				{Copy: &Copy{Src: 0, Len: 1}},
			},
			wantStash: 1,
		},
		{
			name: "identity copy is discarded",
			n:    4,
			delta: []Instruction{
				{Copy: &Copy{Src: 0, Len: 4}},
			},
			wantStash: 0,
		},
		{
			name: "new file longer than old",
			n:    3,
			delta: []Instruction{
				{Copy: &Copy{Src: 0, Len: 3}},
				{Add: &Add{Data: []byte("xyz")}},
			},
			wantStash: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sorter := NewSorter(128, 100)
			plan, err := sorter.Plan(tt.n, tt.delta)
			if err != nil {
				t.Fatal(err)
			}
			if plan.StashBytes != tt.wantStash {
				t.Fatalf("StashBytes=%d want %d; plan=%#v", plan.StashBytes, tt.wantStash, plan.Ops)
			}
			assertExecutes(t, tt.n, tt.delta, plan, expectedFile(tt.n, tt.delta))
		})
	}
}

func TestValidationAndStats(t *testing.T) {
	sorter := NewSorter(4, 1)
	if _, err := sorter.Plan(-1, nil); err != ErrSize {
		t.Fatalf("n<0: %v", err)
	}
	if _, err := sorter.Plan(5, nil); err != ErrSize {
		t.Fatalf("n>MaxSize: %v", err)
	}
	if _, err := sorter.Plan(4, []Instruction{{Copy: &Copy{Src: 3, Len: 2}}}); err != ErrBadDelta {
		t.Fatalf("copy out of bounds: %v", err)
	}
	if _, err := sorter.Plan(4, []Instruction{{Copy: &Copy{Src: 0, Len: 0}}}); err != ErrBadDelta {
		t.Fatalf("zero len: %v", err)
	}
	if _, err := sorter.Plan(4, []Instruction{{Add: &Add{Data: nil}}}); err != ErrBadDelta {
		t.Fatalf("empty add: %v", err)
	}
	if _, err := sorter.Plan(4, []Instruction{{}, {}}); err != ErrBadDelta {
		t.Fatalf("bad instruction: %v", err)
	}
	if _, err := sorter.Plan(4, []Instruction{{Add: &Add{Data: []byte("12345")}}}); err != ErrSize {
		t.Fatalf("m>MaxSize: %v", err)
	}
	if _, err := sorter.Plan(4, []Instruction{{}, {}}); err != ErrBadDelta {
		t.Fatalf("too many instructions: %v", err)
	}

	before := sorter.Stats()
	if _, err := sorter.Plan(4, []Instruction{{Copy: &Copy{Src: 0, Len: 4}}}); err != nil {
		t.Fatal(err)
	}
	after := sorter.Stats()
	if after.AcceptedPlans != before.AcceptedPlans+1 || after.TotalGraphCopies != 0 {
		t.Fatalf("stats=%+v before=%+v", after, before)
	}
}

func TestAddDataIsCopied(t *testing.T) {
	sorter := NewSorter(10, 10)
	input := []byte("abc")
	plan, err := sorter.Plan(3, []Instruction{{Add: &Add{Data: input}}})
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 'z'
	if plan.Ops[0].Data[0] != 'a' {
		t.Fatalf("returned data shares input: %q", plan.Ops[0].Data)
	}
}

func TestConcurrentPlansAndStats(t *testing.T) {
	sorter := NewSorter(32, 20)
	instructionsSets := [][]Instruction{
		{{Copy: &Copy{Src: 5, Len: 5}}, {Copy: &Copy{Src: 0, Len: 5}}},
		{{Add: &Add{Data: []byte("ab")}}, {Copy: &Copy{Src: 0, Len: 8}}},
		{{Copy: &Copy{Src: 3, Len: 3}}, {Copy: &Copy{Src: 6, Len: 3}}, {Copy: &Copy{Src: 0, Len: 3}}},
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < 100; i++ {
				instructions := instructionsSets[(worker+i)%len(instructionsSets)]
				if _, err := sorter.Plan(10, instructions); err != nil {
					t.Errorf("plan: %v", err)
					return
				}
				_ = sorter.Stats()
			}
		}(worker)
	}
	wait.Wait()

	stats := sorter.Stats()
	wantGraphCopies := int64(0)
	for worker := 0; worker < 16; worker++ {
		for i := 0; i < 100; i++ {
			for _, instruction := range instructionsSets[(worker+i)%len(instructionsSets)] {
				if instruction.Copy != nil {
					wantGraphCopies++
				}
			}
		}
	}
	if stats.AcceptedPlans != 1600 || stats.TotalGraphCopies != wantGraphCopies {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func assertPlan(t *testing.T, got, want PlanResult) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func assertExecutes(t *testing.T, n int64, instructions []Instruction, plan PlanResult, want []byte) {
	t.Helper()
	buffer := make([]byte, maxInt64(n, int64(len(want))))
	for i := range buffer {
		buffer[i] = byte('A' + i%26)
	}

	stashes := map[int][]byte{}
	for _, op := range plan.Ops {
		switch op.Kind {
		case OpStash:
			stashes[op.Slot] = append([]byte(nil), buffer[op.Src:op.Src+op.Len]...)
		case OpCopy:
			copy(buffer[op.Dst:op.Dst+op.Len], buffer[op.Src:op.Src+op.Len])
		case OpUnstash:
			copy(buffer[op.Dst:op.Dst+op.Len], stashes[op.Slot])
		case OpAdd:
			copy(buffer[op.Dst:op.Dst+int64(len(op.Data))], op.Data)
		}
	}
	if !bytes.Equal(buffer[:len(want)], want) {
		t.Fatalf("execution mismatch\ngot:  %q\nwant: %q\nplan: %#v", buffer[:len(want)], want, plan.Ops)
	}
}

func expectedFile(n int64, instructions []Instruction) []byte {
	var output []byte
	for _, instruction := range instructions {
		if instruction.Copy != nil {
			for i := int64(0); i < instruction.Copy.Len; i++ {
				output = append(output, byte('A'+(instruction.Copy.Src+i)%26))
			}
		} else {
			output = append(output, instruction.Add.Data...)
		}
	}
	return output
}

func oldData(length int) []byte {
	data := make([]byte, length)
	for i := range data {
		data[i] = byte('A' + i%26)
	}
	return data
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
