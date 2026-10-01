package liveness

import (
	"reflect"
	"testing"
)

// 同一指令先用后定义：x = x + 1 中 x 是使用，进入 UE。
func TestUseBeforeDefInSameInstruction(t *testing.T) {
	order := []int{0}
	spec := map[int]blockSpec{
		0: {insts: []Instruction{inst([]string{"x"}, []string{"x"})}},
	}
	logInput(t, "同一指令先用后定义", order, spec)
	a := buildAnalyzer(t, order, spec)
	assertAgainstNaive(t, a, order, spec)
	got, err := a.EntryLiveIn()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("EntryLiveIn=%v, want [x]", got)
	}
	t.Logf("判定：入口可能使用未定义变量集合=%v", got)
}

// 块内先定义后使用不向上行暴露。
func TestDefBeforeUseNotUpwardExposed(t *testing.T) {
	order := []int{0}
	spec := map[int]blockSpec{
		0: {insts: []Instruction{
			inst(nil, []string{"y"}),
			inst([]string{"y"}, nil),
		}},
	}
	logInput(t, "先定义后使用不上行暴露", order, spec)
	a := buildAnalyzer(t, order, spec)
	assertAgainstNaive(t, a, order, spec)
	snap, _ := a.Block(0)
	if len(snap.UE) != 0 {
		t.Fatalf("UE=%v, want 空", snap.UE)
	}
	if len(snap.LiveIn) != 0 {
		t.Fatalf("LiveIn=%v, want 空", snap.LiveIn)
	}
	t.Log("判定：y 在首次使用前已被定义，UE/LiveIn 均为空")
}

// 多后继取并；无后继块出口为空。
func TestMultipleSuccessorsUnion(t *testing.T) {
	order := []int{0, 1, 2}
	spec := map[int]blockSpec{
		0: {succ: []int{1, 2}},
		1: {insts: []Instruction{inst([]string{"a"}, nil)}},
		2: {insts: []Instruction{inst([]string{"b"}, nil)}},
	}
	logInput(t, "多后继取并", order, spec)
	a := buildAnalyzer(t, order, spec)
	assertAgainstNaive(t, a, order, spec)
	s0, _ := a.Block(0)
	if !reflect.DeepEqual(s0.LiveOut, []string{"a", "b"}) {
		t.Fatalf("B0.LiveOut=%v, want [a b]", s0.LiveOut)
	}
	s1, _ := a.Block(1)
	if len(s1.LiveOut) != 0 {
		t.Fatalf("叶子块 LiveOut=%v, want 空", s1.LiveOut)
	}
	t.Logf("判定：B0.LiveOut=LiveIn(B1)∪LiveIn(B2)=%v；叶子 LiveOut 为空", s0.LiveOut)
}

// 环路收敛：B0 定义 x，B1 使用并再定义 x，B1 跳回 B0。
func TestLoopConvergence(t *testing.T) {
	order := []int{0, 1}
	spec := map[int]blockSpec{
		0: {insts: []Instruction{inst(nil, []string{"x"})}, succ: []int{1}},
		1: {insts: []Instruction{inst([]string{"x"}, []string{"x"})}, succ: []int{0}},
	}
	logInput(t, "环路收敛", order, spec)
	a := buildAnalyzer(t, order, spec)
	assertAgainstNaive(t, a, order, spec)
	s0, _ := a.Block(0)
	if !reflect.DeepEqual(s0.LiveOut, []string{"x"}) {
		t.Fatalf("B0.LiveOut=%v, want [x]", s0.LiveOut)
	}
	if !reflect.DeepEqual(s0.LiveIn, []string{}) {
		t.Fatalf("B0.LiveIn=%v, want 空（x 在 B0 入口即被定义）", s0.LiveIn)
	}
	t.Logf("判定：环上 x 跨迭代使用，经 B1→B0 后向传播使 B0.LiveOut=%v；集合有限轮内不再增长即最小不动点", s0.LiveOut)
}

// 只在一条分支上定义的变量，在汇合处仍活跃。
func TestPartialBranchDefinitionStillLiveAtMerge(t *testing.T) {
	order := []int{0, 1, 2, 3}
	spec := map[int]blockSpec{
		0: {succ: []int{1, 2}},
		1: {insts: []Instruction{inst(nil, []string{"x"})}, succ: []int{3}},
		2: {succ: []int{3}},
		3: {insts: []Instruction{inst([]string{"x"}, nil)}},
	}
	logInput(t, "单分支定义汇合仍活跃", order, spec)
	a := buildAnalyzer(t, order, spec)
	assertAgainstNaive(t, a, order, spec)
	s2, _ := a.Block(2)
	if !reflect.DeepEqual(s2.LiveIn, []string{"x"}) {
		t.Fatalf("B2.LiveIn=%v, want [x]", s2.LiveIn)
	}
	s1, _ := a.Block(1)
	if !reflect.DeepEqual(s1.LiveIn, []string{}) {
		t.Fatalf("B1.LiveIn=%v, want 空（该分支入口即定义 x）", s1.LiveIn)
	}
	s0, _ := a.Block(0)
	if !reflect.DeepEqual(s0.LiveOut, []string{"x"}) {
		t.Fatalf("B0.LiveOut=%v, want [x]", s0.LiveOut)
	}
	t.Log("判定：B2 不定义 x，必须将 x 携带到汇合点 B3；故 B0.LiveOut 仍含 x")
}

// 入口块使用未定义变量。
func TestEntryUsesUndefinedVariable(t *testing.T) {
	order := []int{0}
	spec := map[int]blockSpec{
		0: {insts: []Instruction{
			inst([]string{"a", "b"}, []string{"c"}),
			inst([]string{"c"}, nil),
		}},
	}
	logInput(t, "入口使用未定义变量", order, spec)
	a := buildAnalyzer(t, order, spec)
	assertAgainstNaive(t, a, order, spec)
	got, err := a.EntryLiveIn()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("EntryLiveIn=%v, want [a b]（c 使用前已定义）", got)
	}
	eid, _ := a.EntryID()
	if eid != 0 {
		t.Fatalf("EntryID=%d, want 0", eid)
	}
	t.Logf("判定：入口可能使用未定义变量集合=%v", got)
}

// 前向引用后补块：先引用不存在的后继，补齐后封口成功。
func TestForwardReferenceFilledLater(t *testing.T) {
	a := NewAnalyzer()
	if err := a.AddBlock(5, nil, []int{9}); err != nil {
		t.Fatal(err)
	}
	t.Log("输入：先录入 B5 succ=[9]，B9 此时尚不存在（前向引用，录入阶段允许）")
	if err := a.Seal(); err == nil {
		t.Fatal("B9 未补齐时封口应失败")
	} else {
		assertErrorCode(t, err, ErrMissingSuccessor)
	}
	if err := a.AddBlock(9, []Instruction{inst([]string{"z"}, nil)}, nil); err != nil {
		t.Fatalf("封口失败后应可继续添加: %v", err)
	}
	if err := a.Seal(); err != nil {
		t.Fatalf("补齐后再次封口应成功, got %v", err)
	}
	order := []int{5, 9}
	spec := map[int]blockSpec{
		5: {succ: []int{9}},
		9: {insts: []Instruction{inst([]string{"z"}, nil)}},
	}
	snaps, err := a.Blocks()
	if err != nil {
		t.Fatal(err)
	}
	ref := naiveReference(order, spec)
	for _, s := range snaps {
		rb := ref[s.ID]
		if !reflect.DeepEqual(s.LiveIn, boolKeys(rb.in)) || !reflect.DeepEqual(s.LiveOut, boolKeys(rb.out)) {
			t.Fatalf("B%d = in%v out%v, want in%v out%v", s.ID, s.LiveIn, s.LiveOut, boolKeys(rb.in), boolKeys(rb.out))
		}
	}
	t.Logf("判定：补齐 B9 后重放封口，B5.LiveOut=%v（含 z），与朴素迭代一致", snaps[1].LiveIn)
}
