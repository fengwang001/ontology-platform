package ontology

// 本文件定义前置/后置条件类型及其输入。
//
// 职责分离的关键在于输入类型的结构性隔离：
//   - PreCondition.Eval 只能拿到 PreInput，其中的 State 是执行开始前的
//     已提交快照，无法观察本次执行产生的任何未提交写入；
//   - PostCondition.Eval 只能拿到 PostInput，其中的 State 是
//     “已提交快照 + 最终写入计划”叠加出的统一计划视图；
//   - 引擎只在前置阶段调用 PreCondition、只在后置阶段调用 PostCondition，
//     后置阶段不会重新评估任何前置条件。

// PreInput 是前置条件的判断依据。
type PreInput struct {
	Params map[string]any
	// State 是动作开始执行前已持久化状态的一致快照（只读）。
	State *StateView
}

// PostInput 是后置条件的判断依据。
type PostInput struct {
	Params map[string]any
	// State 是全部写入计划生成完毕后、以统一快照视角叠加出的最终计划状态（只读）。
	State *PlannedView
}

// PreCondition 是一个前置条件。
type PreCondition struct {
	ID string
	// Excludes 声明互斥关系：本条件通过隐含列出的条件必然不通过。
	Excludes []string
	// Descriptor 是条件通过的必要条件（静态元数据），用于定义阶段的
	// 矛盾分析；可以为 nil（退化为真，不参与约束）。
	Descriptor *Expr
	// Eval 在前置阶段基于已提交快照求值。
	Eval func(PreInput) bool
}

// PostCondition 是一个后置条件。
type PostCondition struct {
	ID string
	// Excludes 声明互斥关系，语义同 PreCondition.Excludes。
	Excludes []string
	// Descriptor 同 PreCondition.Descriptor。
	Descriptor *Expr
	// Eval 在后置阶段基于最终计划视图求值。
	Eval func(PostInput) bool
}
