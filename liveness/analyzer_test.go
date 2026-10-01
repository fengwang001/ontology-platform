package liveness

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

type blockSpec struct {
	insts []Instruction
	succ  []int
}

// refBlock 是朴素参照实现中的块。
type refBlock struct {
	ue  map[string]bool
	def map[string]bool
	in  map[string]bool
	out map[string]bool
}

// naiveReference 完全按题目规则写成朴素逐轮迭代：
// UE/Def 顺序扫描指令；LiveOut/LiveIn 从空集开始反复重算，
// 直到一轮无变化，得到最小不动点。
func naiveReference(order []int, spec map[int]blockSpec) map[int]*refBlock {
	blocks := map[int]*refBlock{}
	for _, id := range order {
		s := spec[id]
		b := &refBlock{
			ue:  map[string]bool{},
			def: map[string]bool{},
			in:  map[string]bool{},
			out: map[string]bool{},
		}
		defined := map[string]bool{}
		for _, inst := range s.insts {
			for _, v := range inst.Uses {
				if !defined[v] {
					b.ue[v] = true
				}
			}
			for _, v := range inst.Defs {
				b.def[v] = true
				defined[v] = true
			}
		}
		blocks[id] = b
	}
	ids := append([]int(nil), order...)
	sort.Ints(ids)
	for {
		changed := false
		for _, id := range ids {
			b := blocks[id]
			newOut := map[string]bool{}
			for _, s := range spec[id].succ {
				for v := range blocks[s].in {
					newOut[v] = true
				}
			}
			newIn := map[string]bool{}
			for v := range b.ue {
				newIn[v] = true
			}
			for v := range newOut {
				if !b.def[v] {
					newIn[v] = true
				}
			}
			if !reflect.DeepEqual(newOut, b.out) {
				b.out, changed = newOut, true
			}
			if !reflect.DeepEqual(newIn, b.in) {
				b.in, changed = newIn, true
			}
		}
		if !changed {
			return blocks
		}
	}
}

func boolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func inst(uses, defs []string) Instruction { return Instruction{Uses: uses, Defs: defs} }

func buildAnalyzer(t *testing.T, order []int, spec map[int]blockSpec) *Analyzer {
	t.Helper()
	a := NewAnalyzer()
	for _, id := range order {
		s := spec[id]
		if err := a.AddBlock(id, s.insts, s.succ); err != nil {
			t.Fatalf("AddBlock(%d) 意外失败: %v", id, err)
		}
	}
	return a
}

func logInput(t *testing.T, name string, order []int, spec map[int]blockSpec) {
	t.Helper()
	var sb strings.Builder
	fmt.Fprintf(&sb, "用例【%s】输入（录入顺序 %v）：\n", name, order)
	for _, id := range order {
		s := spec[id]
		fmt.Fprintf(&sb, "  B%d succ=%v\n", id, s.succ)
		for i, in := range s.insts {
			fmt.Fprintf(&sb, "    [%d] uses=%v defs=%v\n", i, in.Uses, in.Defs)
		}
	}
	t.Log(sb.String())
}

// assertAgainstNaive 封口、对照朴素迭代并打印输出与判定依据。
func assertAgainstNaive(t *testing.T, a *Analyzer, order []int, spec map[int]blockSpec) []BlockSnapshot {
	t.Helper()
	if err := a.Seal(); err != nil {
		t.Fatalf("Seal 意外失败: %v", err)
	}
	snaps, err := a.Blocks()
	if err != nil {
		t.Fatalf("Blocks 意外失败: %v", err)
	}
	ref := naiveReference(order, spec)
	var sb strings.Builder
	sb.WriteString("输出（与朴素逐轮迭代逐项一致；依据 UE=定义前使用, Def=全部定义, LiveOut=∪后继LiveIn, LiveIn=UE∪(LiveOut−Def)）：\n")
	for _, s := range snaps {
		fmt.Fprintf(&sb, "  B%d UE=%v Def=%v LiveIn=%v LiveOut=%v\n", s.ID, s.UE, s.Def, s.LiveIn, s.LiveOut)
		rb := ref[s.ID]
		if !reflect.DeepEqual(s.UE, boolKeys(rb.ue)) {
			t.Errorf("B%d UE=%v want %v", s.ID, s.UE, boolKeys(rb.ue))
		}
		if !reflect.DeepEqual(s.Def, boolKeys(rb.def)) {
			t.Errorf("B%d Def=%v want %v", s.ID, s.Def, boolKeys(rb.def))
		}
		if !reflect.DeepEqual(s.LiveIn, boolKeys(rb.in)) {
			t.Errorf("B%d LiveIn=%v want %v", s.ID, s.LiveIn, boolKeys(rb.in))
		}
		if !reflect.DeepEqual(s.LiveOut, boolKeys(rb.out)) {
			t.Errorf("B%d LiveOut=%v want %v", s.ID, s.LiveOut, boolKeys(rb.out))
		}
	}
	t.Log(sb.String())
	return snaps
}

func assertErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际成功", want)
	}
	ae, ok := err.(*AnalysisError)
	if !ok || ae.Code != want {
		t.Fatalf("错误码=%v, want %s", err, want)
	}
	t.Logf("判定：按优先级被拒绝，原因=%s（%s）", want, err)
}

var _ = sync.WaitGroup{}
