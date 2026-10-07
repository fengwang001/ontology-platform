package ontology

// Hooks 提供测试/观测用的同步钩子，全部可选。
type Hooks struct {
	// BeforeCommit 在每次 TryCommit 之前调用。
	BeforeCommit func(a Action, baseline int64)
	// AfterConflict 在一次普通冲突且决定继续重试之后调用，
	// attempt 为已消耗的冲突重试次数（从 1 开始）。
	AfterConflict func(a Action, attempt int)
}

// Executor 执行动作的乐观重试循环。
type Executor struct {
	store *Store
	hooks Hooks
}

// NewExecutor 创建执行器。
func NewExecutor(store *Store, hooks Hooks) *Executor {
	return &Executor{store: store, hooks: hooks}
}

// Run 执行动作直到四种互斥结果之一：
// 成功提交、被更高权限抢占立即终止、重试预算耗尽（普通冲突重试为尝试级结果）。
// 抢占判定在 TryCommit 内先于版本冲突判定，因此也先于重试预算耗尽判定；
// 被抢占立即返回，不消耗重试预算。
func (e *Executor) Run(a Action) Result {
	res := Result{ActionID: a.ID}
	retries := 0
	for {
		snap := e.store.ReadSnapshot(a.ObjectID)
		mut := a.Apply(cloneProps(snap.Props))
		if e.hooks.BeforeCommit != nil {
			e.hooks.BeforeCommit(a, snap.Version)
		}
		outcome, version := e.store.TryCommit(a.ObjectID, a.ID, a.Priority, snap.Version, mut)
		res.Attempts = append(res.Attempts, AttemptRecord{Baseline: snap.Version, Outcome: outcome})
		switch outcome {
		case AttemptCommitted:
			res.Final = Committed
			res.Version = version
			return res
		case AttemptPreempted:
			res.Final = Preempted
			return res
		case AttemptConflict:
			if retries >= a.MaxRetries {
				res.Final = RetriesExhausted
				return res
			}
			retries++
			if e.hooks.AfterConflict != nil {
				e.hooks.AfterConflict(a, retries)
			}
		}
	}
}
