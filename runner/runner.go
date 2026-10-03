// Package runner 管理工作流实例的执行、挂起审批与审计日志。
//
// 步骤以「触发者当前权限 & 工作流上限 & 启动时快照」三者交集运行；
// 权限不足时实例挂起，等待持有审批位（位 63）的第三人放行、拒绝或超时失败。
package runner

import (
	"sync"

	"ontology/flow"
)

// Runner 是工作流运行器。同一运行器内的全部实例共享单调时钟；
// 不同实例可安全并发，所有变更经单锁串行化。
type Runner struct {
	mu       sync.Mutex
	registry MaskView
	catalog  *flow.Catalog
	timeout  int64
	clock    int64
	instMap  map[string]*instance
}

// MaskView 是运行器读取主体当前权限掩码所需的最小能力。
// *grants.Registry 天然满足该接口；测试可注入计数包装核对读取次数。
type MaskView interface {
	Mask(p []byte) uint64
}

// New 创建运行器。g 为权限表，c 为定义表，T 为挂起时限（1..1e9）。
func New(g MaskView, c *flow.Catalog, T int64) (*Runner, error) {
	if g == nil || c == nil || T < 1 || T > maxT {
		return nil, ErrArg
	}
	return &Runner{
		registry: g,
		catalog:  c,
		timeout:  T,
		clock:    0,
		instMap:  make(map[string]*instance),
	}, nil
}

// checkClock 校验 now 范围与单调性。调用方须持锁。
func (r *Runner) checkClock(now int64) error {
	if now < 0 || now > maxNow || now < r.clock {
		return ErrClock
	}
	return nil
}

// expireIfDue 按 now 虚拟地处理到期；commit 为 true 时把 Expire 落库。
// 调用方须持锁。返回实例是否已在到期处理后处于终局。
func (r *Runner) expireIfDue(in *instance, now int64, commit bool) bool {
	if in.phase == PendingPhase && in.suspended && now >= in.deadline {
		if commit {
			in.phase = FailedPhase
			in.outcome = Expired
			in.terminalAt = in.deadline
			in.suspended = false
			in.miss = 0
			in.append(Event{Kind: ExpireEv, Step: in.next, At: in.deadline})
		}
		return true
	}
	return false
}
