package assembler

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

type buildStep struct {
	kind  string
	value int
	label string
}

func buildAssembler(t *testing.T, steps []buildStep) *Assembler {
	t.Helper()
	a := New()
	for _, step := range steps {
		var err error
		switch step.kind {
		case "pad":
			err = a.AddPad(step.value)
		case "jmp":
			err = a.AddJMP(step.label)
		case "jz":
			err = a.AddJZ(step.label)
		case "label":
			err = a.DefineLabel(step.label)
		default:
			t.Fatalf("unknown test step %q", step.kind)
		}
		if err != nil {
			t.Fatalf("setup %v failed: %v", step, err)
		}
	}
	return a
}

func logCase(t *testing.T, name string, steps []buildStep, got *Layout, rounds []LayoutRound, reason string) {
	t.Helper()
	t.Logf("case=%s\ninput=%v\noutput=%#v\nrounds=%#v\n判定依据=%s", name, steps, got, rounds, reason)
}

func naiveJumpEnd(items []Item, long map[int]bool, starts []int, index int) int {
	length := 2
	if long[index] {
		if items[index].Op == JZ {
			length = 6
		} else {
			length = 5
		}
	}
	return starts[index] + length
}

func naiveLayout(items []Item, labels map[string]int) (*Layout, [][]int, error) {
	for index, item := range items {
		if isJump(item.Op) {
			if _, ok := labels[item.Label]; !ok {
				return nil, nil, UndefinedLabelError{InstructionIndex: index, Op: item.Op, Label: item.Label}
			}
		}
	}

	long := make(map[int]bool)
	var changes [][]int
	var starts []int
	var offsets []int
	var total int

	for {
		starts = make([]int, len(items))
		offsets = make([]int, len(items))
		address := 0

		for index, item := range items {
			starts[index] = address
			length := item.Bytes
			if item.Op == JMP {
				length = 2
				if long[index] {
					length = 5
				}
			}
			if item.Op == JZ {
				length = 2
				if long[index] {
					length = 6
				}
			}
			address += length
		}
		total = address

		changed := []int{}
		for index, item := range items {
			if !isJump(item.Op) {
				continue
			}
			targetIndex := labels[item.Label]
			target := total
			if targetIndex < len(starts) {
				target = starts[targetIndex]
			}
			offsets[index] = target - naiveJumpEnd(items, long, starts, index)
			if !long[index] && (offsets[index] < -128 || offsets[index] > 127) {
				changed = append(changed, index)
			}
		}
		changes = append(changes, changed)
		if len(changed) == 0 {
			break
		}
		for _, index := range changed {
			long[index] = true
		}
	}

	instructions := make([]Instruction, len(items))
	for index, item := range items {
		length := item.Bytes
		form := NoForm
		if isJump(item.Op) {
			length = 2
			form = ShortForm
			if long[index] {
				form = LongForm
				if item.Op == JZ {
					length = 6
				} else {
					length = 5
				}
			}
		}
		instructions[index] = Instruction{
			Index:  index,
			Op:     item.Op,
			Label:  item.Label,
			Start:  starts[index],
			Length: length,
			Form:   form,
			Offset: offsets[index],
		}
	}

	result := &Layout{
		Instructions: instructions,
		Labels:       make(map[string]int),
		TotalLength:  total,
	}
	for name, targetIndex := range labels {
		if targetIndex < len(starts) {
			result.Labels[name] = starts[targetIndex]
		} else {
			result.Labels[name] = total
		}
	}
	return result, changes, nil
}

func assertMatchesNaive(t *testing.T, name string, steps []buildStep) (*Layout, []LayoutRound) {
	t.Helper()
	a := buildAssembler(t, steps)
	snapshot := a.Snapshot()
	got, rounds, err := AssembleRounds(snapshot)
	if err != nil {
		t.Fatalf("AssembleRounds failed: %v", err)
	}
	want, naiveChanges, err := naiveLayout(snapshot.Items, snapshot.Labels)
	if err != nil {
		t.Fatalf("naive layout failed: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s layout mismatch\ngot=%#v\nwant=%#v", name, got, want)
	}
	if len(rounds) != len(naiveChanges) {
		t.Fatalf("%s round count mismatch: got %d want %d", name, len(rounds), len(naiveChanges))
	}
	for index := range rounds {
		if !reflect.DeepEqual(rounds[index].Changed, naiveChanges[index]) {
			t.Fatalf("%s round %d changes mismatch: got %v want %v", name, index+1, rounds[index].Changed, naiveChanges[index])
		}
	}
	logCase(t, name, steps, got, rounds, "生产布局、偏移、总长度和逐轮提升集合均与朴素逐轮迭代一致")
	return got, rounds
}

func TestRelaxationBoundaryCases(t *testing.T) {
	cases := []struct {
		name  string
		steps []buildStep
	}{
		{
			name: "forward offset exactly 127 stays short",
			steps: []buildStep{
				{kind: "jmp", label: "t"},
				{kind: "pad", value: 127},
				{kind: "label", label: "t"},
			},
		},
		{
			name: "forward offset exactly 128 becomes long",
			steps: []buildStep{
				{kind: "jmp", label: "t"},
				{kind: "pad", value: 128},
				{kind: "label", label: "t"},
			},
		},
		{
			name: "backward offset exactly -128 stays short",
			steps: []buildStep{
				{kind: "label", label: "t"},
				{kind: "pad", value: 126},
				{kind: "jmp", label: "t"},
			},
		},
		{
			name: "backward offset exactly -129 becomes long",
			steps: []buildStep{
				{kind: "label", label: "t"},
				{kind: "pad", value: 127},
				{kind: "jmp", label: "t"},
			},
		},
		{
			name: "one lengthening pushes another exactly over boundary",
			steps: []buildStep{
				{kind: "jmp", label: "t"},
				{kind: "jz", label: "u"},
				{kind: "pad", value: 122},
				{kind: "label", label: "t"},
				{kind: "pad", value: 1},
				{kind: "pad", value: 5},
				{kind: "label", label: "u"},
			},
		},
		{
			name: "two jumps cross boundary together",
			steps: []buildStep{
				{kind: "jmp", label: "x"},
				{kind: "jmp", label: "y"},
				{kind: "pad", value: 128},
				{kind: "label", label: "y"},
				{kind: "pad", value: 2},
				{kind: "label", label: "x"},
			},
		},
		{
			name: "label at end",
			steps: []buildStep{
				{kind: "pad", value: 1},
				{kind: "label", label: "end"},
			},
		},
		{
			name: "jump to itself",
			steps: []buildStep{
				{kind: "label", label: "self"},
				{kind: "jmp", label: "self"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout, rounds := assertMatchesNaive(t, tc.name, tc.steps)
			switch tc.name {
			case "forward offset exactly 127 stays short":
				assertInstruction(t, layout.Instructions[0], 2, ShortForm, 127)
			case "forward offset exactly 128 becomes long":
				assertInstruction(t, layout.Instructions[0], 5, LongForm, 128)
				if layout.TotalLength != 133 {
					t.Fatalf("total length = %d, want 133", layout.TotalLength)
				}
			case "backward offset exactly -128 stays short":
				assertInstruction(t, layout.Instructions[1], 2, ShortForm, -128)
			case "backward offset exactly -129 becomes long":
				assertInstruction(t, layout.Instructions[1], 5, LongForm, -132)
				if layout.TotalLength != 132 {
					t.Fatalf("total length = %d, want 132", layout.TotalLength)
				}
			case "one lengthening pushes another exactly over boundary":
				if !reflect.DeepEqual(rounds[0].Changed, []int{1}) || !reflect.DeepEqual(rounds[1].Changed, []int{0}) {
					t.Fatalf("chain rounds = %#v, want [1] then [0]", rounds)
				}
				assertInstruction(t, layout.Instructions[0], 5, LongForm, 128)
				assertInstruction(t, layout.Instructions[1], 6, LongForm, 128)
			case "two jumps cross boundary together":
				if !reflect.DeepEqual(rounds[0].Changed, []int{0, 1}) {
					t.Fatalf("first round changes = %v, want both jumps", rounds[0].Changed)
				}
			case "label at end":
				if layout.Labels["end"] != 1 || layout.TotalLength != 1 {
					t.Fatalf("end label layout = %#v", layout)
				}
			case "jump to itself":
				assertInstruction(t, layout.Instructions[0], 2, ShortForm, -2)
			}
		})
	}
}

func assertInstruction(t *testing.T, instruction Instruction, length int, form Form, offset int) {
	t.Helper()
	if instruction.Length != length || instruction.Form != form || instruction.Offset != offset {
		t.Fatalf("instruction = %#v, want length=%d form=%s offset=%d", instruction, length, form, offset)
	}
}

func TestRejectedOperationsDoNotMutateState(t *testing.T) {
	a := New()
	if err := a.AddPad(0); !errors.Is(err, ErrInvalidPadSize) {
		t.Fatalf("pad 0 error = %v, want %v", err, ErrInvalidPadSize)
	}
	if err := a.AddPad(1001); !errors.Is(err, ErrInvalidPadSize) {
		t.Fatalf("pad 1001 error = %v, want %v", err, ErrInvalidPadSize)
	}
	if err := a.DefineLabel("x"); err != nil {
		t.Fatalf("DefineLabel x failed: %v", err)
	}
	if err := a.DefineLabel("x"); !errors.Is(err, ErrLabelDefined) {
		t.Fatalf("duplicate label error = %v, want %v", err, ErrLabelDefined)
	}
	if err := a.DefineLabel(""); !errors.Is(err, ErrEmptyLabel) {
		t.Fatalf("empty label error = %v, want %v", err, ErrEmptyLabel)
	}

	if err := a.AddJMP("missing"); err != nil {
		t.Fatalf("AddJMP failed: %v", err)
	}
	_, err := a.Assemble()
	var undefined UndefinedLabelError
	if !errors.As(err, &undefined) || undefined.InstructionIndex != 0 {
		t.Fatalf("undefined error = %v, want earliest index 0", err)
	}

	snapshot := a.Snapshot()
	if len(snapshot.Items) != 1 || snapshot.Items[0].Op != JMP {
		t.Fatalf("rejected operations changed items: %#v", snapshot.Items)
	}
	if !reflect.DeepEqual(snapshot.Labels, map[string]int{"x": 0}) {
		t.Fatalf("rejected operations changed labels: %#v", snapshot.Labels)
	}
	t.Logf("input=invalid pads, duplicate x, empty label, JMP missing\noutput=%v %#v\n判定依据=错误可区分；空名检查在已定义检查前；失败操作后仍只有一条 JMP 和标签 x", err, snapshot)
}

func TestUndefinedLabelReportsEarliestInstruction(t *testing.T) {
	a := buildAssembler(t, []buildStep{
		{kind: "pad", value: 1},
		{kind: "jmp", label: "later"},
		{kind: "pad", value: 1},
		{kind: "jz", label: "earlier-missing"},
	})
	_, err := a.Assemble()
	var undefined UndefinedLabelError
	if !errors.As(err, &undefined) {
		t.Fatalf("error = %v, want UndefinedLabelError", err)
	}
	if undefined.InstructionIndex != 1 || undefined.Op != JMP || undefined.Label != "later" {
		t.Fatalf("earliest undefined mismatch: %#v", undefined)
	}

	before := a.Snapshot()
	_, err = a.Assemble()
	after := a.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed assembly left state change: before=%#v after=%#v", before, after)
	}
	t.Logf("input=PAD, JMP later, PAD, JZ earlier-missing\noutput=%v\n判定依据=按指令下标扫描，先报告下标 1；失败前后快照相同", err)
}

func TestRepeatedAssemblyIsPureAndReplayDeterministic(t *testing.T) {
	steps := []buildStep{
		{kind: "jmp", label: "t"},
		{kind: "pad", value: 200},
		{kind: "jmp", label: "t"},
		{kind: "label", label: "t"},
	}
	first := buildAssembler(t, steps)
	firstSnapshot := first.Snapshot()
	layout1, rounds1, err := AssembleRounds(firstSnapshot)
	if err != nil {
		t.Fatalf("first assembly failed: %v", err)
	}
	layout2, rounds2, err := AssembleRounds(firstSnapshot)
	if err != nil {
		t.Fatalf("repeat assembly failed: %v", err)
	}
	if !reflect.DeepEqual(layout1, layout2) || !reflect.DeepEqual(rounds1, rounds2) {
		t.Fatalf("repeated assembly on same snapshot is not deterministic")
	}

	layout1.Instructions[0].Start = 999
	layout1.Labels["t"] = 999
	layout3, err := first.Assemble()
	if err != nil {
		t.Fatalf("assembly after mutating returned layout failed: %v", err)
	}
	if !reflect.DeepEqual(layout3, layout2) {
		t.Fatalf("returned layout mutation affected assembler")
	}

	second := buildAssembler(t, steps)
	replayed, err := second.Assemble()
	if err != nil {
		t.Fatalf("replay assembly failed: %v", err)
	}
	if !reflect.DeepEqual(replayed, layout2) {
		t.Fatalf("same operation sequence replay mismatch")
	}

	if err := first.AddPad(3); err != nil {
		t.Fatalf("AddPad failed: %v", err)
	}
	appended, err := first.Assemble()
	if err != nil {
		t.Fatalf("appended assembly failed: %v", err)
	}
	if appended.TotalLength == layout2.TotalLength || appended.TotalLength != layout2.TotalLength+3 {
		t.Fatalf("appended total = %d, old total = %d", appended.TotalLength, layout2.TotalLength)
	}
	t.Logf("input=%v plus PAD 3\noutput=%#v appended=%#v\n判定依据=同快照重复结果一致；修改返回布局不影响内部；相同操作重放一致；追加后得到新布局", steps, layout2, appended)
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	a := New()
	const workers = 12
	var wg sync.WaitGroup
	var resultsMu sync.Mutex
	var results []*Layout
	var snapshots []Snapshot

	for worker := 0; worker < workers; worker++ {
		wg.Add(4)
		go func(worker int) {
			defer wg.Done()
			if err := a.AddPad(1); err != nil {
				t.Errorf("concurrent AddPad failed: %v", err)
			}
		}(worker)
		go func(worker int) {
			defer wg.Done()
			name := "label" + string(rune('a'+worker))
			if err := a.DefineLabel(name); err != nil {
				t.Errorf("concurrent DefineLabel %s failed: %v", name, err)
			}
		}(worker)
		go func(worker int) {
			defer wg.Done()
			layout, err := a.Assemble()
			if err != nil {
				t.Errorf("concurrent Assemble failed: %v", err)
				return
			}
			resultsMu.Lock()
			results = append(results, layout)
			resultsMu.Unlock()
		}(worker)
		go func(worker int) {
			defer wg.Done()
			snapshot := a.Snapshot()
			resultsMu.Lock()
			snapshots = append(snapshots, snapshot)
			resultsMu.Unlock()
		}(worker)
	}
	wg.Wait()

	final := a.Snapshot()
	if len(final.Items) != workers {
		t.Fatalf("concurrent pads = %d, want %d; items=%#v", len(final.Items), workers, final.Items)
	}
	for _, layout := range results {
		if layout.TotalLength < 0 || layout.TotalLength > workers || layout.TotalLength != len(layout.Instructions) {
			t.Fatalf("non-serializable observed total length: %d", layout.TotalLength)
		}
	}
	for _, snapshot := range snapshots {
		if len(snapshot.Items) < 0 || len(snapshot.Items) > workers {
			t.Fatalf("non-serializable snapshot item count: %d", len(snapshot.Items))
		}
		if len(snapshot.Labels) < 0 || len(snapshot.Labels) > workers {
			t.Fatalf("non-serializable snapshot label count: %d", len(snapshot.Labels))
		}
		for name, index := range snapshot.Labels {
			if index < 0 || index > len(snapshot.Items) || name == "" {
				t.Fatalf("non-serializable snapshot label %q=%d items=%d", name, index, len(snapshot.Items))
			}
		}
	}
	t.Logf("input=%d concurrent AddPad, DefineLabel, Assemble and Snapshot workers\noutput=items=%d labels=%d layouts=%d snapshots=%d\n判定依据=追加/标签互斥、汇编/查询读锁深拷贝；最终数量精确且观察值均满足合法串行前缀约束", workers, len(final.Items), len(final.Labels), len(results), len(snapshots))
}
