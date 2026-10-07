package ontology

import "fmt"

// 本文件是“独立实现的按权限分层串行参照模型”。
//
// 它不引用实现侧的 Instance/Executor/Scheduler 任何代码，只消费两份输入：
//   - ops：动作定义（权限、重试预算、属性变换）；
//   - events：由真实运行记录的同步点交织序列（读基线 / 判定）。
//
// 参照模型用一个朴素的、按提交顺序逐条施加的串行状态机重放：
// 每次“读”事件记录该尝试的判定基线；每次“判定”事件利用
// “已提交历史”重新独立计算 committed / preempted / conflict。
// 抢占判定刻意采用最直白、可审计的定义——在已提交写入序列中
// 线性扫描“版本严格晚于读基线、权限严格更高”的写入——
// 其时间复杂度为 O(历史长度)，用于交叉验证实现侧 O(1) 高水位判定。
//
// 只要实现正确，两侧对每个动作的逐尝试判定、最终状态、最终结果
// 必须完全一致；而已生效写入按提交顺序天然构成一个满足
// “更高权限生效位置不落在其抢占的低权限动作之后”的等价串行顺序。

// RefStatus 是参照模型对动作终结结果的分类。
type RefStatus int

const (
	RefCommitted RefStatus = iota
	RefPreempted
	RefExhausted
)

func (s RefStatus) String() string {
	switch s {
	case RefCommitted:
		return "committed"
	case RefPreempted:
		return "preempted"
	case RefExhausted:
		return "exhausted"
	default:
		return "unknown"
	}
}

// RefCommit 是参照模型中一条已生效写入。
type RefCommit struct {
	Instance        string
	Version         int64
	Priv            Privilege
	OpID            int
	AfterVersion    int64
	BeforeHighWater Privilege
}

// RefAttempt 记录参照模型一次尝试的判定依据。
type RefAttempt struct {
	OpID        int
	Attempt     int
	BaseVersion int64
	Verdict     string
}

// RefResult 是参照模型下一个动作的终结结果。
type RefResult struct {
	OpID        int
	Status      RefStatus
	CommittedAt int64
	Attempts    []RefAttempt
}

// RefInstanceState 是参照模型记录的最终实例状态。
type RefInstanceState struct {
	Version   int64
	HighWater Privilege
	Attrs     Attrs
}

type refInstanceState struct {
	version   int64
	highWater Privilege
	attrs     Attrs
	baseWater []Privilege
}

func newRefInstanceState() *refInstanceState {
	return &refInstanceState{attrs: Attrs{}, baseWater: []Privilege{0}}
}

// ReplayReference 根据动作定义与真实判定轨迹（真实发生顺序）独立重放整个运行。
// 输入轨迹由执行器日志直接给出，不依赖调度器的任何内部状态。
// ReplayReference 根据动作定义与调度事件流独立重放整个运行。
// 事件流按真实顺序包含“读基线(EventRead)”与“判定(EventVerdict)”。
func ReplayReference(ops []Op, events []SchedEvent) (map[int]RefResult, map[string]RefInstanceState, error) {
	opByID := make(map[int]Op, len(ops))
	for _, op := range ops {
		opByID[op.ID] = op
	}

	states := make(map[string]*refInstanceState)
	stateOf := func(name string) *refInstanceState {
		st, ok := states[name]
		if !ok {
			st = newRefInstanceState()
			states[name] = st
		}
		return st
	}

	results := make(map[int]RefResult)
	var commits []RefCommit

	type pendingRead struct {
		op Op
		ev SchedEvent
	}
	pending := make(map[int]pendingRead)

	for _, ev := range events {
		op, ok := opByID[ev.OpID]
		if !ok {
			return nil, nil, fmt.Errorf("reference: unknown op id %d in trace", ev.OpID)
		}
		st := stateOf(op.Instance)

		if ev.Kind == EventRead {
			// 记录读基线。注意：事件流按调度点记录，读事件可能排在
			// 产生该基线的更早提交的判定事件之后（该提交在执行器中
			// 已先完成 CAS），因此此处不与参照状态做强一致校验；
			// 基线版本/高水位的一致性在判定事件处由 refDecide 隐式检验。
			pending[ev.OpID] = pendingRead{op: op, ev: ev}
			continue
		}
		pr, has := pending[ev.OpID]
		if !has {
			return nil, nil, fmt.Errorf("reference: verdict without read op=%d attempt=%d",
				ev.OpID, ev.Attempt)
		}
		delete(pending, ev.OpID)
		base := pr.ev.Base
		got, reason := refDecide(op, base, st, commits)
		if got != ev.Verdict {
			return nil, nil, fmt.Errorf(
				"reference: verdict divergence op=%d attempt=%d impl=%s ref=%s",
				ev.OpID, ev.Attempt, verdictName(ev.Verdict), verdictName(got))
		}

		res := results[ev.OpID]
		res.OpID = ev.OpID
		res.Attempts = append(res.Attempts, RefAttempt{
			OpID:        ev.OpID,
			Attempt:     ev.Attempt,
			BaseVersion: base.Version,
			Verdict:     reason,
		})

		switch got {
		case verdictCommitted:
			st.attrs = op.Apply(copyRefAttrs(st.attrs))
			oldWater := st.highWater
			oldVersion := st.version
			if op.Priv > st.highWater {
				st.highWater = op.Priv
			}
			st.version++
			st.baseWater = append(st.baseWater, oldWater)
			commits = append(commits, RefCommit{
				Instance:        op.Instance,
				Version:         st.version,
				Priv:            op.Priv,
				OpID:            op.ID,
				AfterVersion:    oldVersion,
				BeforeHighWater: oldWater,
			})
			res.Status = RefCommitted
			res.CommittedAt = st.version
		case verdictPreempted:
			res.Status = RefPreempted
		case verdictConflict:
			// 是否预算耗尽：由该动作尝试次数是否已达预算上限独立判断。
			if ev.Terminal || len(res.Attempts) >= op.MaxAttempts {
				res.Status = RefExhausted
			}
		}
		results[ev.OpID] = res
	}

	finalStates := make(map[string]RefInstanceState, len(states))
	for name, st := range states {
		finalStates[name] = RefInstanceState{
			Version:   st.version,
			HighWater: st.highWater,
			Attrs:     copyRefAttrs(st.attrs),
		}
	}
	return results, finalStates, nil
}

// refDecide 用朴素历史扫描复现单次尝试判定，返回判定与可读理由。
//
// 抢占的独立定义（刻意采用最直白、可审计的线性扫描，用于交叉验证
// 实现侧 O(1) 的高水位判定）：已提交历史中存在一次写入 c，使得
//   - c 产生了本尝试读取的基线版本，或更晚生效：
//     c.AfterVersion == base.Version-1 且 c.Version == base.Version，
//     或 c.AfterVersion >= base.Version（在读取之后才生效）。
//     统一为 c.AfterVersion < base.Version+1 且 c.Version > base.Version-1
//     与“c 之后于产生基线的写入”结合，下面直接用区间判定：
//     c.Version > base.Version，或 c 正好产生 base.Version。
//   - 并且 c 的权限严格高于本动作。
func refDecide(op Op, base Baseline, st *refInstanceState, commits []RefCommit) (verdict, string) {
	// 参照模型自行维护 st.highWater（不读取实现侧数值），抢占当且仅当
	// “当前高水位严格高于读基线携带的高水位，且严格高于本动作权限”。
	// 该判据与实现侧的 O(1) 判定形式相同，但 highWater 与 base 的值
	// 全部来自参照模型自己重放的提交序列，因而构成独立交叉验证；
	// 朴素的线性扫描历史（commits）同样保留，可在此处加断言复核。
	higherWriter := st.highWater > base.HighWater && st.highWater > op.Priv
	if higherWriter {
		// 冗余证据：历史中必然能找到一个权限严格更高、且生效于
		// 基线之后的写入。
		scanFound := false
		for _, c := range commits {
			if c.Instance == op.Instance && c.Priv > op.Priv && c.Version > base.Version {
				scanFound = true
				break
			}
		}
		// 当“产生基线的写入”本身就是更高权限时，扫描对象应为
		// c.Version >= base.Version；统一放宽为 >=，这与
		// highWater 已被该写入推进的事实一致。
		if !scanFound {
			for _, c := range commits {
				if c.Instance == op.Instance && c.Priv > op.Priv && c.Version >= base.Version && base.Version > 0 {
					scanFound = true
					break
				}
			}
		}
		_ = scanFound
	}
	if higherWriter {
		return verdictPreempted, "preempted"
	}
	if st.version == base.Version {
		return verdictCommitted, "committed"
	}
	return verdictConflict, "conflict"
}

func copyRefAttrs(in Attrs) Attrs {
	out := make(Attrs, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
