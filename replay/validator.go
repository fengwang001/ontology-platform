package replay

// Validator 为差异记录重放校验组件的编排入口。
// 其方法不持有任何可变状态，可安全并发调用。
type Validator struct{}

// NewValidator 构造校验组件。
func NewValidator() *Validator { return &Validator{} }

// Validate 按固定优先级依次执行四个阶段：
//  1. 差异记录自身自洽性校验（不依赖快照）
//  2. 重放前提环境校验（对快照只读）
//  3. 在写时复制覆盖层上重放
//  4. 重放结果与声明目标的逐项等价性核对
//
// 命中靠前类别后立即返回，不再继续后续判定。
// 全过程对 snapshot 只读，可并发调用且结果确定。
func (v *Validator) Validate(snapshot *Snapshot, delta *Delta) Verdict {
	view := &snapshotView{snap: snapshot}

	self, err := checkDeltaSelfConsistency(delta)
	if err != nil {
		return fail(DeltaInconsistent, *err, view.stats())
	}

	if err := checkPreconditions(view, self); err != nil {
		return fail(PreconditionUnmet, *err, view.stats())
	}

	final, err := replayChanges(view, delta)
	if err != nil {
		// 重放期失败说明差异记录声明的变化无法组合成合法状态，
		// 归类为差异记录自身不自洽（此时前提环境已确认满足）。
		return fail(DeltaInconsistent, *err, view.stats())
	}

	if err := checkEquivalence(final, self); err != nil {
		return fail(NotEquivalent, *err, view.stats())
	}

	return Verdict{
		Category:    Valid,
		Location:    NoLocation,
		ChangeIndex: -1,
		Reason:      "重放结果与差异记录声明的目标状态在结构、对象、链接三个层面逐项一致",
		Stats:       view.stats(),
	}
}

func fail(c Category, f failure, stats Stats) Verdict {
	return Verdict{
		Category:    c,
		Location:    f.location,
		ChangeIndex: f.changeIndex,
		Reason:      f.reason,
		Stats:       stats,
	}
}

// failure 为各阶段返回的内部失败描述。
type failure struct {
	location    Location
	changeIndex int
	reason      string
}
