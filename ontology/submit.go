package ontology

// Reason 是单次尝试命中的判定原因。
type Reason int

const (
	ReasonCommitted Reason = iota
	ReasonVersionConflict
	ReasonCardinality
	ReasonRetriesExhausted
)

// AttemptDecision 完整记录一次内部尝试读取到的关联状态、判定依据与结果。
type AttemptDecision struct {
	Attempt    int
	Read       *Snapshot
	Reason     Reason
	Violations []Violation
}

// Result 是一次逻辑写入请求的最终结果与完整尝试轨迹。
type Result struct {
	ObjectID  string
	Committed bool
	Version   int64
	Attempts  []AttemptDecision
	Reason    Reason
}

// RetryPolicy 限定内部重试预算：最多 MaxAttempts 次尝试（含首次）。
type RetryPolicy struct {
	MaxAttempts int
}

func DefaultRetryPolicy() RetryPolicy { return RetryPolicy{MaxAttempts: 1} }

// AttemptHook 在每一次内部尝试“已完成新鲜读取、尚未进入实例锁
// 复核/提交”的窗口被调用，供测试确定性地制造并发交错
// （例如“别人先抢走唯一名额”、“基数在重试间反复变化”）。
// 参数 read 是本次尝试新读到的快照（前一次的快照不会被复用）。
// 生产路径下恒为 nil，判定不依赖任何额外对外暴露的状态。
type AttemptHook func(change Change, attempt int, read *Snapshot)

// Submitter 在 Store 上以乐观方式提交带基数约束的更新，
// 每次重试都重新读取最新基线与全部关联。
type Submitter struct {
	store  *Store
	policy RetryPolicy
	hook   AttemptHook
}

func NewSubmitter(store *Store, policy RetryPolicy) *Submitter {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	return &Submitter{store: store, policy: policy}
}

// WithHook 返回绑定尝试钩子的提交器（主要用于测试）。
func (u *Submitter) WithHook(hook AttemptHook) *Submitter {
	return &Submitter{store: u.store, policy: u.policy, hook: hook}
}

// Submit 执行一次逻辑写入，返回完整判定轨迹或三类互斥错误之一。
func (u *Submitter) Submit(change Change) (*Result, error) {
	result := &Result{ObjectID: change.ObjectID}

	// 每次迭代都是一次全新的尝试：Store.attempt 在实例锁内
	// 重新读取当前基线版本与当前实际持有的全部链接关联，
	// 前一次尝试的快照、计数只进入记录，绝不参与本次判定。
	for attempt := 1; attempt <= u.policy.MaxAttempts; attempt++ {
		// 每次尝试都重新读取目标实例当前的基线版本与当前实际
		// 持有的全部链接关联。第一次尝试使用调用方基线；之后
		// 的每次重试使用上一行刚刚读取到的最新版本作为本次
		// 期望值，绝不把前一次尝试的关联集合或计数带入判定。
		fresh := u.store.Snapshot(change.ObjectID)
		// 第一次尝试以调用方基线为期望版本；重试以本次新读版本为期望。
		expectedVersion := change.BaseVersion
		if attempt > 1 {
			expectedVersion = fresh.Version
		}

		if u.hook != nil {
			u.hook(change, attempt, fresh)
		}

		outcome := u.store.attempt(change, expectedVersion)
		decision := AttemptDecision{
			Attempt:    attempt,
			Read:       cloneSnapshot(outcome.read),
			Reason:     outcome.reason,
			Violations: append([]Violation(nil), outcome.violations...),
		}
		result.Attempts = append(result.Attempts, decision)

		switch outcome.reason {
		case ReasonCommitted:
			result.Committed = true
			result.Version = outcome.version
			result.Reason = ReasonCommitted
			return result, nil

		case ReasonCardinality:
			// 版本匹配且在本次最新读取下基数仍不满足：
			// 这是业务拒绝，立即终止；不再重试，也不算重试耗尽。
			result.Reason = ReasonCardinality
			return result, &CardinalityError{
				ObjectID:   change.ObjectID,
				Violations: outcome.violations,
			}

		case ReasonVersionConflict:
			if attempt < u.policy.MaxAttempts {
				continue
			}
			// 最后一次尝试仍冲突。若调用方从未获得过一次新鲜基线
			// （MaxAttempts==1，或整个预算只有一次尝试的等价情形），
			// 按版本冲突返回；否则属于在重试中被连续抢先至预算耗尽。
			if u.policy.MaxAttempts == 1 {
				result.Reason = ReasonVersionConflict
				return result, &VersionConflictError{
					ObjectID:      change.ObjectID,
					BaseVersion:   change.BaseVersion,
					LatestVersion: outcome.read.Version,
				}
			}
			// 已达最后一次尝试。判定顺序第 3 位：只有在
			// 版本冲突（而非基数不满足）终结最后一次尝试时，
			// 才归因为重试耗尽——明确告知调用方是预算用完，
			// 而不是业务规则本身不满足。
			result.Reason = ReasonRetriesExhausted
			return result, &RetriesExhaustedError{
				ObjectID:    change.ObjectID,
				Attempts:    attempt,
				MaxAttempts: u.policy.MaxAttempts,
			}
		}
	}

	// 不可达：循环内必然返回。
	panic("ontology: submit loop exited without decision")
}
