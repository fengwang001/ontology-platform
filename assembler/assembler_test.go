package assembler

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type testOp struct {
	kind  OpKind
	label string
	pad   int
}

type oracleEntry struct {
	start  int
	size   int
	form   Form
	offset int
	target int
}

func TestBranchRelaxationBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		ops    []testOp
		labels map[string]int
		check  func(*testing.T, *Result)
	}{
		{
			name: "forward 127 remains short",
			ops: []testOp{
				{kind: JMP, label: "end"},
				{kind: Pad, pad: 127},
			},
			labels: map[string]int{"end": 2},
			check: func(t *testing.T, result *Result) {
				entry := result.Entries[0]
				if entry.Form != ShortForm || entry.Offset != 127 || result.TotalLength != 129 {
					t.Fatalf("got form=%v offset=%d total=%d", entry.Form, entry.Offset, result.TotalLength)
				}
			},
		},
		{
			name: "forward 128 relaxes JMP",
			ops: []testOp{
				{kind: JMP, label: "end"},
				{kind: Pad, pad: 128},
			},
			labels: map[string]int{"end": 2},
			check: func(t *testing.T, result *Result) {
				entry := result.Entries[0]
				if entry.Form != LongForm || entry.Size != 5 || entry.Offset != 128 || result.TotalLength != 133 || result.Rounds != 1 {
					t.Fatalf("got form=%v size=%d offset=%d total=%d rounds=%d",
						entry.Form, entry.Size, entry.Offset, result.TotalLength, result.Rounds)
				}
			},
		},
		{
			name: "backward -128 remains short",
			ops: []testOp{
				{kind: Pad, pad: 126},
				{kind: JMP, label: "start"},
			},
			labels: map[string]int{"start": 0},
			check: func(t *testing.T, result *Result) {
				entry := result.Entries[1]
				if entry.Form != ShortForm || entry.Offset != -128 || result.TotalLength != 128 {
					t.Fatalf("got form=%v offset=%d", entry.Form, entry.Offset)
				}
			},
		},
		{
			name: "backward -129 relaxes JMP",
			ops: []testOp{
				{kind: Pad, pad: 127},
				{kind: JMP, label: "start"},
			},
			labels: map[string]int{"start": 0},
			check: func(t *testing.T, result *Result) {
				entry := result.Entries[1]
				if entry.Form != LongForm || entry.Size != 5 || entry.Offset != -132 || result.TotalLength != 132 {
					t.Fatalf("got form=%v size=%d offset=%d total=%d", entry.Form, entry.Size, entry.Offset, result.TotalLength)
				}
			},
		},
		{
			name: "one relaxation pushes another jump exactly over",
			ops: []testOp{
				{kind: JZ, label: "forward"},
				{kind: Pad, pad: 125},
				{kind: JMP, label: "start"},
			},
			labels: map[string]int{"start": 0, "forward": 3},
			check: func(t *testing.T, result *Result) {
				first := result.Entries[0]
				second := result.Entries[2]
				if first.Form != LongForm || first.Offset != 130 {
					t.Fatalf("first = %+v", first)
				}
				if second.Form != LongForm || second.Offset != -136 {
					t.Fatalf("second = %+v", second)
				}
				if result.Rounds != 2 || result.TotalLength != 136 {
					t.Fatalf("rounds=%d total=%d", result.Rounds, result.TotalLength)
				}
			},
		},
		{
			name: "two jumps exceed in the same round",
			ops: []testOp{
				{kind: JMP, label: "end"},
				{kind: JZ, label: "end"},
				{kind: Pad, pad: 128},
			},
			labels: map[string]int{"end": 3},
			check: func(t *testing.T, result *Result) {
				first := result.Entries[0]
				second := result.Entries[1]
				if first.Form != LongForm || first.Offset != 134 {
					t.Fatalf("first = %+v", first)
				}
				if second.Form != LongForm || second.Size != 6 || second.Offset != 128 {
					t.Fatalf("second = %+v", second)
				}
				if result.Rounds != 1 || result.TotalLength != 139 {
					t.Fatalf("rounds=%d total=%d", result.Rounds, result.TotalLength)
				}
			},
		},
		{
			name: "label is bound at the end",
			ops: []testOp{
				{kind: Pad, pad: 7},
				{kind: JMP, label: "end"},
			},
			labels: map[string]int{"end": 2},
			check: func(t *testing.T, result *Result) {
				if result.Labels["end"] != 9 || result.TotalLength != 9 || result.Entries[1].Offset != 0 {
					t.Fatalf("label=%d total=%d offset=%d", result.Labels["end"], result.TotalLength, result.Entries[1].Offset)
				}
			},
		},
		{
			name: "jump to self is negative short length",
			ops: []testOp{
				{kind: JZ, label: "self"},
				{kind: Pad, pad: 1},
			},
			labels: map[string]int{"self": 0},
			check: func(t *testing.T, result *Result) {
				entry := result.Entries[0]
				if entry.Form != ShortForm || entry.Offset != -2 || result.TotalLength != 3 {
					t.Fatalf("entry=%+v total=%d", entry, result.TotalLength)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := buildTestAssembler(t, tc.ops, tc.labels)
			result, err := a.Assemble()
			if err != nil {
				t.Fatalf("assemble: %v", err)
			}
			compareOracle(t, result, tc.ops, tc.labels)
			if tc.name == "backward -129 relaxes JMP" {
				initialForms := []Form{NoForm, ShortForm}
				initial, _, _ := oracleLayout(tc.ops, tc.labels, initialForms)
				if initial[1].offset != -129 {
					t.Fatalf("initial short backward offset = %d, want -129", initial[1].offset)
				}
			}
			tc.check(t, result)
			logLayout(t, tc.name, tc.ops, tc.labels, result, "boundary offset, form and total match naive rounds")
		})
	}
}

func buildTestAssembler(t *testing.T, ops []testOp, labels map[string]int) *Assembler {
	t.Helper()
	a := New()
	names := make([]string, len(ops)+1)
	for name, index := range labels {
		names[index] = name
	}
	for index := range ops {
		if names[index] != "" {
			if err := a.DefineLabel(names[index]); err != nil {
				t.Fatalf("define %q: %v", names[index], err)
			}
		}
		switch op := ops[index]; op.kind {
		case Pad:
			if err := a.AppendPad(op.pad); err != nil {
				t.Fatalf("append PAD %d: %v", op.pad, err)
			}
		case JMP, JZ:
			if err := a.AppendJump(op.kind, op.label); err != nil {
				t.Fatalf("append %v %q: %v", op.kind, op.label, err)
			}
		}
	}
	if names[len(ops)] != "" {
		if err := a.DefineLabel(names[len(ops)]); err != nil {
			t.Fatalf("define %q: %v", names[len(ops)], err)
		}
	}
	return a
}

func oracleSize(kind OpKind, form Form) int {
	longSize := 6
	if kind == JMP {
		longSize = 5
	}
	if form == LongForm {
		return longSize
	}
	return 2
}

func oracleLayout(ops []testOp, labels map[string]int, forms []Form) ([]oracleEntry, map[string]int, int) {
	resolved := make(map[string]int)
	for name, boundIndex := range labels {
		if boundIndex == 0 {
			resolved[name] = 0
		}
	}

	entries := make([]oracleEntry, len(ops))
	address := 0
	for index, op := range ops {
		size := op.pad
		if op.kind == JMP || op.kind == JZ {
			size = oracleSize(op.kind, forms[index])
		}
		entries[index] = oracleEntry{start: address, size: size, form: forms[index], target: -1}
		address += size
		for name, boundIndex := range labels {
			if boundIndex == index+1 {
				resolved[name] = address
			}
		}
	}

	for index, op := range ops {
		if op.kind == JMP || op.kind == JZ {
			entries[index].target = resolved[op.label]
			entries[index].offset = entries[index].target - (entries[index].start + entries[index].size)
		}
	}
	return entries, resolved, address
}

func oracleAssemble(ops []testOp, labels map[string]int) ([]oracleEntry, map[string]int, int, int) {
	forms := make([]Form, len(ops))
	for index, op := range ops {
		if op.kind != Pad {
			forms[index] = ShortForm
		}
	}

	rounds := 0
	for {
		entries, resolved, total := oracleLayout(ops, labels, forms)
		nextForms := append([]Form(nil), forms...)
		changed := false
		for index, entry := range entries {
			if entry.form == ShortForm && (entry.offset < -128 || entry.offset > 127) {
				nextForms[index] = LongForm
				changed = true
			}
		}
		if !changed {
			return entries, resolved, total, rounds
		}
		forms = nextForms
		rounds++
	}
}

func compareOracle(t *testing.T, result *Result, ops []testOp, labels map[string]int) {
	t.Helper()
	entries, _, total, rounds := oracleAssemble(ops, labels)
	if result.TotalLength != total || result.Rounds != rounds || len(result.Entries) != len(entries) {
		t.Fatalf("oracle mismatch: got total=%d rounds=%d entries=%d, want total=%d rounds=%d entries=%d",
			result.TotalLength, result.Rounds, len(result.Entries), total, rounds, len(entries))
	}
	for index, expected := range entries {
		actual := result.Entries[index]
		if actual.Start != expected.start || actual.Size != expected.size || actual.Form != expected.form ||
			actual.Offset != expected.offset || actual.Target != expected.target {
			t.Fatalf("entry %d oracle mismatch: got %+v, want %+v", index, actual, expected)
		}
	}
}

func logLayout(t *testing.T, name string, ops []testOp, labels map[string]int, result *Result, reason string) {
	t.Helper()
	var builder strings.Builder
	fmt.Fprintf(&builder, "input ops=%v labels=%v\n", ops, labels)
	builder.WriteString("output entries:")
	for _, entry := range result.Entries {
		fmt.Fprintf(&builder, " %+v", entry)
	}
	fmt.Fprintf(&builder, "\nlabels=%v total=%d rounds=%d\njudgment: %s", result.Labels, result.TotalLength, result.Rounds, reason)
	t.Logf("%s\n%s", name, builder.String())
}

func TestErrorsAndAtomicRejections(t *testing.T) {
	a := New()
	err := a.AppendPad(0)
	if !errors.Is(err, ErrInvalidPadSize) {
		t.Fatalf("PAD 0 error = %v, want ErrInvalidPadSize", err)
	}
	var invalidPad InvalidPadSizeError
	if !errors.As(err, &invalidPad) || invalidPad.Size != 0 {
		t.Fatalf("errors.As InvalidPadSizeError failed: %#v", err)
	}
	if err := a.AppendPad(1001); !errors.Is(err, ErrInvalidPadSize) {
		t.Fatalf("PAD 1001 error = %v", err)
	}
	if got := a.Len(); got != 0 {
		t.Fatalf("Len after invalid PAD = %d, want 0", got)
	}

	if err := a.DefineLabel(""); !errors.Is(err, ErrEmptyLabelName) {
		t.Fatalf("empty label error = %v, want ErrEmptyLabelName", err)
	}
	if err := a.DefineLabel("x"); err != nil {
		t.Fatalf("define x: %v", err)
	}
	if err := a.DefineLabel(""); !errors.Is(err, ErrEmptyLabelName) {
		t.Fatalf("empty duplicate label should report empty first, got %v", err)
	}
	if err := a.DefineLabel("x"); !errors.Is(err, ErrLabelAlreadyDefined) {
		t.Fatalf("duplicate label error = %v", err)
	}

	_ = a.AppendJump(JMP, "missing-later")
	_ = a.AppendJump(JZ, "missing-earlier")
	_, err = a.Assemble()
	var undefined UndefinedLabelError
	if !errors.As(err, &undefined) || !errors.Is(err, ErrUndefinedLabel) {
		t.Fatalf("assembly error = %v, want undefined label", err)
	}
	if undefined.OperationIndex != 0 || undefined.Label != "missing-later" {
		t.Fatalf("undefined report = %+v, want first operation", undefined)
	}

	snapshot := a.Snapshot()
	if len(snapshot.Operations) != 2 || len(snapshot.Labels) != 1 {
		t.Fatalf("state after rejected operations = %+v", snapshot)
	}
	snapshot.Operations[0].Label = "mutated"
	if again := a.Snapshot(); again.Operations[0].Label != "missing-later" {
		t.Fatalf("snapshot mutation changed assembler state: %+v", again)
	}
}

func TestRepeatedAssemblyAndReplay(t *testing.T) {
	ops := []testOp{
		{kind: JZ, label: "forward"},
		{kind: Pad, pad: 125},
		{kind: JMP, label: "start"},
	}
	labels := map[string]int{"start": 0, "forward": 3}
	first := buildTestAssembler(t, ops, labels)
	firstResult, err := first.Assemble()
	if err != nil {
		t.Fatalf("first assembly: %v", err)
	}
	secondResult, err := first.Assemble()
	if err != nil {
		t.Fatalf("second assembly: %v", err)
	}
	if fmt.Sprint(firstResult) != fmt.Sprint(secondResult) {
		t.Fatal("repeated assembly produced a different result")
	}

	replayed := buildTestAssembler(t, ops, labels)
	replayedResult, err := replayed.Assemble()
	if err != nil {
		t.Fatalf("replayed assembly: %v", err)
	}
	if fmt.Sprint(firstResult.Entries) != fmt.Sprint(replayedResult.Entries) ||
		firstResult.TotalLength != replayedResult.TotalLength ||
		firstResult.Rounds != replayedResult.Rounds {
		t.Fatal("replaying the same operations produced a different layout")
	}
	logLayout(t, "pure fixed point replay", ops, labels, firstResult, "repeat assembly and identical replay return equal entries, rounds and total")
}

func TestConcurrentOperationsAndQueries(t *testing.T) {
	const goroutines = 16
	var group sync.WaitGroup
	var assemblerErr error
	var errMu sync.Mutex

	for worker := 0; worker < goroutines; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			local := New()
			label := fmt.Sprintf("label-%d", worker)
			if err := local.AppendJump(JMP, label); err != nil {
				errMu.Lock()
				assemblerErr = err
				errMu.Unlock()
				return
			}
			if err := local.AppendPad(10 + worker); err != nil {
				errMu.Lock()
				assemblerErr = err
				errMu.Unlock()
				return
			}
			if err := local.DefineLabel(label); err != nil {
				errMu.Lock()
				assemblerErr = err
				errMu.Unlock()
			}
		}(worker)
	}

	shared := New()
	for worker := 0; worker < goroutines; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			label := fmt.Sprintf("shared-%d", worker)
			if err := shared.AppendJump(JZ, label); err != nil {
				errMu.Lock()
				assemblerErr = err
				errMu.Unlock()
				return
			}
			if err := shared.AppendPad(1); err != nil {
				errMu.Lock()
				assemblerErr = err
				errMu.Unlock()
				return
			}
			if err := shared.DefineLabel(label); err != nil {
				errMu.Lock()
				assemblerErr = err
				errMu.Unlock()
				return
			}
			_ = shared.Snapshot().Operations
			_, _ = shared.Assemble()
		}(worker)
	}

	group.Wait()
	if assemblerErr != nil {
		t.Fatalf("concurrent operation: %v", assemblerErr)
	}
	if got := shared.Len(); got != goroutines*2 {
		t.Fatalf("shared Len = %d, want %d", got, goroutines*2)
	}
	result, err := shared.Assemble()
	if err != nil {
		t.Fatalf("final shared assembly: %v", err)
	}
	if result.TotalLength != goroutines*3 || len(result.Entries) != goroutines*2 {
		t.Fatalf("shared result total=%d entries=%d", result.TotalLength, len(result.Entries))
	}
}
