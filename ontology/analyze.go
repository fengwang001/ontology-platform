package ontology

import "fmt"

// 本文件实现动作声明的矛盾静态分析。
//
// 一个动作的全部前置条件必须同时通过（全部后置条件同理），因此：
//   - 若同一阶段内存在声明的互斥对（A 通过隐含 B 不通过），则该阶段
//     的条件集合永远无法自洽地全部通过 —— 声明自相矛盾；
//   - 若同一阶段全部条件描述子的合取不可满足，则不存在任何输入能让
//     全部条件通过 —— 声明自相矛盾。
//
// 分析只在动作定义（注册）阶段执行一次，结果缓存在 ActionType 中；
// 开销只与条件数量及描述子原子数相关，与历史调用次数无关（由
// Registry.analysisRuns 计数器与测试共同验证）。

// Contradiction 描述一处声明自相矛盾。
type Contradiction struct {
	Phase Phase
	// Conditions 是涉及矛盾的条件 ID。
	Conditions []string
	Reason     string
}

func (c *Contradiction) Error() string {
	return fmt.Sprintf("ontology: action declaration contradiction in %s phase (%v): %s",
		c.Phase, c.Conditions, c.Reason)
}

// DeclarationAnalysis 是对一个动作声明的完整分析结果。
type DeclarationAnalysis struct {
	Contradiction *Contradiction
	// 统计信息，用于验证分析开销只与声明本身相关。
	PreAtoms      int
	PostAtoms     int
	PreAssigns    int64
	PostAssigns   int64
	PreSATSkipped bool
	PostSATSkip   bool
}

// condMeta 把两种条件类型抽象出分析所需的公共字段。
type condMeta struct {
	id         string
	excludes   []string
	descriptor *Expr
}

func preMeta(c PreCondition) condMeta   { return condMeta{c.ID, c.Excludes, c.Descriptor} }
func postMeta(c PostCondition) condMeta { return condMeta{c.ID, c.Excludes, c.Descriptor} }

// analyzePhase 分析单个阶段的条件集合，返回矛盾（若有）与 SAT 统计。
func analyzePhase(phase Phase, conds []condMeta) (*Contradiction, satResult) {
	byID := make(map[string]condMeta, len(conds))
	for _, c := range conds {
		byID[c.id] = c
	}
	// 1) 互斥检查：自斥、以及同一阶段内任何互斥对都使“全部通过”不可能。
	for _, c := range conds {
		for _, ex := range c.excludes {
			if ex == c.id {
				return &Contradiction{
					Phase:      phase,
					Conditions: []string{c.id},
					Reason:     "condition excludes itself",
				}, satResult{}
			}
			if _, ok := byID[ex]; ok {
				return &Contradiction{
					Phase:      phase,
					Conditions: []string{c.id, ex},
					Reason: fmt.Sprintf(
						"%q passing implies %q fails, but both are required to pass",
						c.id, ex),
				}, satResult{}
			}
		}
	}
	// 2) 描述子合取的可满足性。
	var exprs []*Expr
	for _, c := range conds {
		if c.descriptor != nil {
			exprs = append(exprs, c.descriptor)
		}
	}
	sat := conjunctionSAT(exprs)
	if !sat.Satisfiable {
		ids := make([]string, 0, len(conds))
		for _, c := range conds {
			if c.descriptor != nil {
				ids = append(ids, c.id)
			}
		}
		return &Contradiction{
			Phase:      phase,
			Conditions: ids,
			Reason:     "condition descriptors are jointly unsatisfiable: no input allows all conditions in this phase to pass",
		}, sat
	}
	return nil, sat
}

// analyzeDeclaration 对完整动作声明做矛盾分析。
func analyzeDeclaration(pre []PreCondition, post []PostCondition) DeclarationAnalysis {
	var out DeclarationAnalysis
	preConds := make([]condMeta, len(pre))
	for i, c := range pre {
		preConds[i] = preMeta(c)
	}
	postConds := make([]condMeta, len(post))
	for i, c := range post {
		postConds[i] = postMeta(c)
	}
	preContra, preSAT := analyzePhase(PhasePre, preConds)
	out.PreAtoms, out.PreAssigns, out.PreSATSkipped = preSAT.Atoms, preSAT.Assignments, preSAT.Skipped
	if preContra != nil {
		out.Contradiction = preContra
		return out
	}
	postContra, postSAT := analyzePhase(PhasePost, postConds)
	out.PostAtoms, out.PostAssigns, out.PostSATSkip = postSAT.Atoms, postSAT.Assignments, postSAT.Skipped
	if postContra != nil {
		out.Contradiction = postContra
	}
	return out
}

// validateDeclaration 做结构性校验（ID 唯一、互斥引用存在、Eval 非空），
// 返回普通错误而非矛盾。
func validateDeclaration(at *ActionType) error {
	if at.ID == "" {
		return fmt.Errorf("ontology: action type ID must not be empty")
	}
	seen := make(map[string]bool)
	check := func(phase Phase, id string, hasEval bool) error {
		if id == "" {
			return fmt.Errorf("ontology: action %q has a %s condition with empty ID", at.ID, phase)
		}
		key := phase.String() + "/" + id
		if seen[key] {
			return fmt.Errorf("ontology: action %q has duplicate %s condition ID %q", at.ID, phase, id)
		}
		seen[key] = true
		if !hasEval {
			return fmt.Errorf("ontology: action %q %s condition %q has nil Eval", at.ID, phase, id)
		}
		return nil
	}
	for _, c := range at.Preconditions {
		if err := check(PhasePre, c.ID, c.Eval != nil); err != nil {
			return err
		}
	}
	for _, c := range at.Postconditions {
		if err := check(PhasePost, c.ID, c.Eval != nil); err != nil {
			return err
		}
	}
	// 互斥引用必须指向同阶段已声明的条件。
	preIDs := make(map[string]bool)
	for _, c := range at.Preconditions {
		preIDs[c.ID] = true
	}
	for _, c := range at.Preconditions {
		for _, ex := range c.Excludes {
			if !preIDs[ex] {
				return fmt.Errorf("ontology: action %q pre condition %q excludes unknown condition %q", at.ID, c.ID, ex)
			}
		}
	}
	postIDs := make(map[string]bool)
	for _, c := range at.Postconditions {
		postIDs[c.ID] = true
	}
	for _, c := range at.Postconditions {
		for _, ex := range c.Excludes {
			if !postIDs[ex] {
				return fmt.Errorf("ontology: action %q post condition %q excludes unknown condition %q", at.ID, c.ID, ex)
			}
		}
	}
	return nil
}
