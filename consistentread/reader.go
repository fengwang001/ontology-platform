// Package consistentread 提供带滞后上限的一致性快照读取器。
package consistentread

import "sync"

// Position 是单调推进的位点，逐字段比较即视为同一个位点。
// Index 是日志中的连续偏移；Epoch 是该偏移提交时的纪元（如主从任期），
// 允许随提交向前跳跃但不得倒退。
type Position struct {
	Epoch int64
	Index int64
}

// Mode 决定滞后约束无法满足时的读取行为。
type Mode int

const (
	// ModeDegrade 不等待：立即返回当前已应用位点，并报告是否降级。
	ModeDegrade Mode = iota
	// ModeBlock 冻结调用时刻的提交位点，阻塞等待已应用位点追上去。
	ModeBlock
)

// Snapshot 是一次读取返回的可复现结果。
type Snapshot struct {
	Position Position
	// Degraded 为 true 表示返回位点落后于调用时刻的提交位点，
	// 仅在 ModeDegrade 下可能出现；ModeBlock 恒为 false。
	Degraded bool
	// Reason 记录本次判定依据，便于日志核对。
	Reason string
}

// FailureReason 标识一次操作被整体拒绝的可区分原因。
type FailureReason string

const (
	ReasonNegativeLag         FailureReason = "negative max lag"
	ReasonCommitNotAdvanced   FailureReason = "commit position must advance monotonically"
	ReasonCommitNotContiguous FailureReason = "commit position is not contiguous"
	ReasonApplyAheadOfCommit  FailureReason = "applied position is ahead of committed position"
	ReasonApplyOutOfOrder     FailureReason = "applied position must advance monotonically"
	ReasonUnknownPosition     FailureReason = "position was never committed"
)

// Failure 携带可区分的拒绝原因；任何失败都不改变内部状态。
type Failure struct {
	Op     string
	Reason FailureReason
	Detail string
}

func (f *Failure) Error() string {
	if f.Detail == "" {
		return "consistentread: " + f.Op + ": " + string(f.Reason)
	}
	return "consistentread: " + f.Op + ": " + string(f.Reason) + ": " + f.Detail
}

// Reader 维护提交位点与已应用位点，提供滞后有界的一致性读取。
type Reader struct {
	mu        sync.Mutex
	cond      *sync.Cond
	started   bool
	committed Position
	applied   Position
	appliedOn bool
	// epochAt[i] 是已提交偏移 i 对应的纪元，用于核对应用位点确实被提交过。
	epochAt []int64
}

// NewReader 创建读取器，初始位点为零值（尚未提交任何位点）。
func NewReader() *Reader {
	r := &Reader{}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// Positions 以逐字段一致的快照返回当前提交位点与已应用位点。
func (r *Reader) Positions() (committed, applied Position) {
	r.mu.Lock()
	committed, applied = r.committed, r.applied
	r.mu.Unlock()
	return
}

// Check 是并发安全的自检：核对已应用位点恒不超过提交位点这一内部不变量。
func (r *Reader) Check() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started && less(r.committed, r.applied) {
		return &Failure{
			Op:     "check",
			Reason: ReasonApplyAheadOfCommit,
			Detail: formatPositions(r.committed, r.applied),
		}
	}
	return nil
}

// AdvanceCommit 单调、连续地推进提交位点。
// 第一个提交位点的 Index 必须为 0，之后每次必须恰好推进 1 个偏移；
// Epoch 不得倒退。任何拒绝都不会改变已有位点与等待者。
func (r *Reader) AdvanceCommit(next Position) error {
	if next.Epoch < 0 || next.Index < 0 {
		return &Failure{Op: "advance-commit", Reason: ReasonCommitNotAdvanced, Detail: "negative field"}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		if next.Index != 0 {
			return &Failure{Op: "advance-commit", Reason: ReasonCommitNotContiguous, Detail: "first commit must start at index 0"}
		}
		r.started = true
		r.committed = next
		r.epochAt = append(r.epochAt, next.Epoch)
		r.cond.Broadcast()
		return nil
	}

	if !less(r.committed, next) {
		return &Failure{
			Op:     "advance-commit",
			Reason: ReasonCommitNotAdvanced,
			Detail: formatPositions(r.committed, next),
		}
	}
	if next.Index != r.committed.Index+1 {
		return &Failure{
			Op:     "advance-commit",
			Reason: ReasonCommitNotContiguous,
			Detail: formatPositions(r.committed, next),
		}
	}
	if next.Epoch < r.committed.Epoch {
		return &Failure{
			Op:     "advance-commit",
			Reason: ReasonCommitNotAdvanced,
			Detail: "epoch must not regress",
		}
	}

	r.committed = next
	r.epochAt = append(r.epochAt, next.Epoch)
	// 提交推进同样唤醒等待者：它们会自行复核自己冻结的目标是否已被应用。
	r.cond.Broadcast()
	return nil
}

// AdvanceApply 单调推进已应用位点，且不得超过提交位点。
// next 必须是当前已应用位点的下一个连续偏移，且该偏移确实已被提交
// （纪元逐字段一致）。任何拒绝都不会改变已有位点与等待者。
func (r *Reader) AdvanceApply(next Position) error {
	if next.Epoch < 0 || next.Index < 0 {
		return &Failure{Op: "advance-apply", Reason: ReasonApplyOutOfOrder, Detail: "negative field"}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || next.Index > r.committed.Index {
		return &Failure{
			Op:     "advance-apply",
			Reason: ReasonApplyAheadOfCommit,
			Detail: formatPositions(r.committed, next),
		}
	}

	var expectedIndex int64
	if r.appliedOn {
		expectedIndex = r.applied.Index + 1
	}
	if next.Index != expectedIndex {
		return &Failure{
			Op:     "advance-apply",
			Reason: ReasonApplyOutOfOrder,
			Detail: formatPositions(r.applied, next),
		}
	}
	if next.Epoch != r.epochAt[next.Index] {
		return &Failure{
			Op:     "advance-apply",
			Reason: ReasonUnknownPosition,
			Detail: formatPositions(Position{Epoch: r.epochAt[next.Index], Index: next.Index}, next),
		}
	}

	r.applied = next
	r.appliedOn = true
	r.cond.Broadcast()
	return nil
}

// Read 按最大滞后 maxLag 在提交位点附近读取一个可复现的快照。
// 滞后以连续偏移个数计（committed.Index - applied.Index）。
//
// ModeDegrade：绝不等待，立即返回当前已应用位点；当滞后超过 maxLag 时
// Degraded 为 true，并在 Reason 中给出判定依据。
//
// ModeBlock：在进入调用的瞬间冻结提交位点 target；若已应用位点未达
// target 则阻塞，直到被推进唤醒为止。此后提交位点再推进也不会改变
// 本次冻结的 target，返回位点恒等于 target，且 Degraded 恒为 false。
func (r *Reader) Read(mode Mode, maxLag int64) (Snapshot, error) {
	if maxLag < 0 {
		return Snapshot{}, &Failure{Op: "read", Reason: ReasonNegativeLag, Detail: "maxLag must be >= 0"}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return Snapshot{}, &Failure{Op: "read", Reason: ReasonUnknownPosition, Detail: "nothing committed yet"}
	}

	switch mode {
	case ModeDegrade:
		lag := r.committed.Index - r.applied.Index
		snap := Snapshot{Position: r.applied}
		if lag > maxLag {
			snap.Degraded = true
			snap.Reason = "degrade: lag " + itoa(lag) + " > maxLag " + itoa(maxLag) +
				"; return applied immediately"
		} else if lag > 0 {
			snap.Reason = "within bound: lag " + itoa(lag) + " <= maxLag " + itoa(maxLag) +
				"; return applied"
		} else {
			snap.Reason = "strong: lag 0 <= maxLag " + itoa(maxLag) + "; applied equals committed"
		}
		return snap, nil

	case ModeBlock:
		// 在持锁的调用瞬间冻结目标；后续提交推进不影响该局部变量。
		target := r.committed
		for less(r.applied, target) {
			r.cond.Wait()
		}
		return Snapshot{
			Position: target,
			Degraded: false,
			Reason:   "block: frozen target reached; later commits do not move this read",
		}, nil

	default:
		return Snapshot{}, &Failure{Op: "read", Reason: FailureReason("unknown mode"), Detail: itoa(int64(mode))}
	}
}

func less(a, b Position) bool {
	if a.Epoch != b.Epoch {
		return a.Epoch < b.Epoch
	}
	return a.Index < b.Index
}

func formatPositions(a, b Position) string {
	return "{e" + itoa(a.Epoch) + ",i" + itoa(a.Index) + "} -> {e" +
		itoa(b.Epoch) + ",i" + itoa(b.Index) + "}"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
