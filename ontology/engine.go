package ontology

// DefaultMaxAttempts 是同一逻辑写入请求内部尝试次数的默认上限。
const DefaultMaxAttempts = 5

// SettleHook 允许测试在“本次尝试读取最新状态”与“本次尝试提交”之间注入并发事件，
// 例如让其他请求抢先提交，从而确定性地制造版本冲突/基数变化。生产环境传 nil。
type SettleHook func(req Request, attempt int, readVersion uint64)

// Engine 在 Store 之上提供带有限次重试的乐观写入。
type Engine struct {
	store       *Store
	maxAttempts int
	beforeTry   SettleHook
}

// NewEngine 创建引擎。maxAttempts<=0 时使用默认上限。
func NewEngine(store *Store, maxAttempts int, hook SettleHook) *Engine {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	return &Engine{store: store, maxAttempts: maxAttempts, beforeTry: hook}
}

// MaxAttempts 返回内部尝试次数上限。
func (e *Engine) MaxAttempts() int { return e.maxAttempts }

// Update 执行一次逻辑写入请求。
//
// 每次尝试都重新读取目标实例当前版本与全部链接；判定优先级为
// 版本冲突 > 基数校验 > 重试耗尽。
// 任何失败的中间尝试都不改变版本、链接或时钟状态。
func (e *Engine) Update(req Request) (*Result, error) {
	if req.Instance == "" {
		return nil, &ErrInvalid{Msg: "empty instance"}
	}
	if len(req.Ops) == 0 {
		return nil, &ErrInvalid{Msg: "no ops"}
	}

	res := &Result{}
	// 第一次尝试使用调用方基线；第 2 次及以后的期望值在当次重新读取后就地刷新，
	// 从结构上保证不携带任何历史尝试读到的版本或关联数据。

	for attempt := 1; attempt <= e.maxAttempts; attempt++ {
		// 1) 每次尝试前重新读取当前基线版本（不把任何关联集合/计数带入后续判定）。
		readVersion, err := e.store.readVersion(req.Instance)
		if err != nil {
			return nil, err
		}
		expected := req.Baseline
		if attempt > 1 {
			expected = readVersion
		}

		// 2) 注入并发事件（测试用，生产为 nil）：其他请求可能在此刻抢先提交。
		if e.beforeTry != nil {
			e.beforeTry(req, attempt, readVersion)
		}

		// 3) 判定优先级第 1 位：版本冲突优先。以锁内权威 CAS 确认基线是否落后。
		cr, err := e.store.tryCommit(req.Instance, expected, req.Ops)
		if err != nil {
			return nil, err
		}

		// 记录统一取“判定时点”的同一时点重读：版本、全量链接、判定依据一致、可重放。
		freshVersion, _, freshSnapshot, ferr := e.store.readForAttempt(req.Instance, req.Ops)
		if ferr != nil {
			return nil, ferr
		}
		rec := AttemptRecord{Index: attempt, VersionRead: freshVersion, Snapshot: freshSnapshot}

		switch {
		case cr.committed:
			// 唯一允许改变外部可观察状态的路径；锁内已完成逐约束校验。
			rec.Outcome = OutcomeCommitted
			rec.Verdicts = copyVerdicts(cr.verdicts)
			res.Attempts = append(res.Attempts, rec)
			res.Committed = true
			res.Version = cr.version
			return res, nil

		case cr.cardKey != nil:
			// 版本匹配但最新读取下基数不满足：业务拒绝为终态。
			rec.Outcome = OutcomeCardinality
			rec.Verdicts = copyVerdicts(cr.verdicts)
			res.Attempts = append(res.Attempts, rec)
			k := *cr.cardKey
			res.Reject = &Rejection{
				Code:       CodeCardinality,
				Message:    "cardinality constraint violated after fresh re-check",
				Constraint: &k,
			}
			return res, nil

		default:
			// CAS 因基线落后失败：版本冲突命中，本次尝试零变更。
			// 审计记录（上方统一重读）与当次判定依据都取自提交时点的最新状态。
			_, verdicts, _, err := e.store.checkCardinality(req.Instance, req.Ops)
			if err != nil {
				return nil, err
			}
			rec.Outcome = OutcomeConflict
			rec.Verdicts = copyVerdicts(verdicts)
			res.Attempts = append(res.Attempts, rec)
			if attempt >= e.maxAttempts {
				// 判定优先级第 3 位：预算花完后才归为重试耗尽。
				res.Reject = &Rejection{
					Code:    CodeRetriesExhausted,
					Message: "optimistic update rejected: retry budget exhausted after repeated version conflicts",
				}
				return res, nil
			}
		}
	}

	// 理论上循环内必定返回；此处给出防御性兜底（重试耗尽）。
	res.Reject = &Rejection{Code: CodeRetriesExhausted, Message: "retry budget exhausted"}
	return res, nil
}

func copyVerdicts(vs []ConstraintVerdict) []ConstraintVerdict {
	out := make([]ConstraintVerdict, len(vs))
	copy(out, vs)
	return out
}
