// Package vote 实现两阶段提交第一阶段的投票收集与裁决规则。
//
// 裁决规则：
//   - 所有参与者都投同意票 => 提交（Commit）。
//   - 任一参与者投否决票 => 全体中止，原因记为 Rejected。
//   - 截止时刻已过而仍有参与者未投票 => 全体中止，原因记为 TimedOut。
//
// 本包只做纯逻辑裁决，不依赖时钟、goroutine 或其他包。
package vote

// Outcome 是单个参与者的投票结果。
type Outcome int

const (
	// Agree 表示参与者同意提交。
	Agree Outcome = iota
	// Reject 表示参与者否决。
	Reject
)

// Decision 是事务的最终决议。
type Decision int

const (
	// None 表示尚未裁决（零值，绝不作为猜测结果出现）。
	None Decision = iota
	// Commit 表示全票同意，进入提交。
	Commit
	// Abort 表示存在否决或超时，全体中止。
	Abort
)

// Cause 是中止决议的原因，可区分「被否决」与「超时」。
type Cause int

const (
	// NoCause 表示无原因（决议为 None 或 Commit 时）。
	NoCause Cause = iota
	// Rejected 表示因某参与者投否决票而中止。
	Rejected
	// TimedOut 表示因某参与者未在截止时刻前投票而中止。
	TimedOut
)

// Verdict 是一次裁决的结果。零值表示「尚未裁决」。
type Verdict struct {
	Decision Decision
	Cause    Cause
	// Culprit 指出导致中止的参与者 ID；决议为 None 或 Commit 时为空串。
	Culprit string
}

// String 便于日志与演示程序打印。
func (v Verdict) String() string {
	switch v.Decision {
	case Commit:
		return "COMMIT"
	case Abort:
		if v.Cause == Rejected {
			return "ABORT(rejected by " + v.Culprit + ")"
		}
		return "ABORT(timeout waiting for " + v.Culprit + ")"
	default:
		return "PENDING"
	}
}

// Decide 按注册顺序检查每个参与者的投票并给出裁决。
//
// order 是参与者 ID 的注册顺序（保证裁决确定性）；votes 是已收到的票。
// expired 表示第一阶段截止时刻是否已过（由调用方按注入时钟判定）。
//
// 规则：遇到否决票立即裁 Abort/Rejected；遇到缺席票时，若已过期则裁
// Abort/TimedOut，否则返回零值 Verdict 表示继续等待；全部同意则裁 Commit。
// 参与者集合为空时裁 Commit（空集上「全票同意」 vacuously 成立）。
func Decide(order []string, votes map[string]Outcome, expired bool) Verdict {
	for _, id := range order {
		outcome, ok := votes[id]
		if ok && outcome == Reject {
			return Verdict{Decision: Abort, Cause: Rejected, Culprit: id}
		}
		if !ok {
			if expired {
				return Verdict{Decision: Abort, Cause: TimedOut, Culprit: id}
			}
			return Verdict{}
		}
	}
	return Verdict{Decision: Commit}
}
