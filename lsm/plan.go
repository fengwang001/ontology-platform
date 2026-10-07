package lsm

import "fmt"

// PlanKind 区分压实计划的类型。
type PlanKind int

const (
	// PlanRewrite 重写：本层输入与下一层重叠文件合并重写。
	PlanRewrite PlanKind = iota
	// PlanMove 直接下移：本层只有一个输入文件且下一层无任何重叠文件，
	// 该文件直接改挂到目标层，不重写数据。
	PlanMove
)

func (k PlanKind) String() string {
	if k == PlanMove {
		return "move"
	}
	return "rewrite"
}

// Plan 描述一次待执行的压实：哪些文件参与、压到哪一层、为什么。
// 计划创建时其全部输入立即被占用，直到安装成功或被取消。
type Plan struct {
	ID          uint64
	Level       int // 源层
	TargetLevel int // 目标层，恒为 Level+1
	Kind        PlanKind
	// Inputs 源层输入文件：零层计划按 ID 升序，非零层计划按 (Smallest, Largest, ID) 升序。
	Inputs []FileMeta
	// NextInputs 目标层被纳入的重叠文件（含边界闭合结果），按 (Smallest, Largest, ID) 升序。
	NextInputs []FileMeta
	// Score 源层被选中时的精确分数（"分子/分母" 形式）。
	Score string
	// Reason 人类可读的判定依据，便于精确复现“为什么压这些文件”。
	Reason string
}

// inputInterval 返回一组文件的合并闭区间。
func inputInterval(files []FileMeta) interval {
	var in interval
	for _, f := range files {
		in.extend(f.Smallest, f.Largest)
	}
	return in
}

// allInputs 返回计划的全部输入（源层 + 目标层）。
func (p *Plan) allInputs() []FileMeta {
	out := make([]FileMeta, 0, len(p.Inputs)+len(p.NextInputs))
	out = append(out, p.Inputs...)
	out = append(out, p.NextInputs...)
	return out
}

// containsInput 报告给定文件编号是否是本计划的输入。
func (p *Plan) containsInput(id uint64) bool {
	for _, f := range p.allInputs() {
		if f.ID == id {
			return true
		}
	}
	return false
}

func (p *Plan) String() string {
	return fmt.Sprintf("plan#%d L%d->L%d %s inputs=%v next=%v score=%s",
		p.ID, p.Level, p.TargetLevel, p.Kind, fileIDs(p.Inputs), fileIDs(p.NextInputs), p.Score)
}

func fileIDs(files []FileMeta) []uint64 {
	ids := make([]uint64, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	return ids
}
