package snapshot

import (
	"sort"
	"sync"
)

// Coordinator 是多卷一致性组快照协调服务。
//
// 并发模型：所有公开操作先获取同一把互斥锁，因此任意并发调用都等价于
// 某个串行顺序（锁的获取顺序即线性化点）。服务内部不使用墙上时钟与随机数，
// 时刻完全由操作携带，因此相同操作序列重放得到完全相同的结果。
//
// 每个操作的统一判定管线（见 ops.go 中各方法）：
//  1. 参数非法检查（不依赖状态的部分）；
//  2. 时钟回退检查，通过后接受该时刻并推进时钟；
//  3. 超时检测（时间推进的结果，可能触发自动中止，不算被拒绝操作造成的状态改变）；
//  4. 各操作自身的判定（不存在、组冲突、状态错误、重复确认、队列已满）。
type Coordinator struct {
	mu sync.Mutex

	cfg Config

	clockStarted bool
	lastTime     Time

	volumes map[string]*volume
	groups  map[string]*group

	// active 保存当前有进行中快照的组（冻结中或已冻结），
	// 超时检测只扫描这些组。
	active map[string]*group
}

// NewCoordinator 创建协调服务。MaxHoldDuration 为负属于配置错误，会 panic。
func NewCoordinator(cfg Config) *Coordinator {
	if cfg.MaxHoldDuration < 0 {
		panic("snapshot: MaxHoldDuration must be >= 0")
	}
	return &Coordinator{
		cfg:     cfg,
		volumes: make(map[string]*volume),
		groups:  make(map[string]*group),
		active:  make(map[string]*group),
	}
}

// begin 是统一判定管线的第 2、3 步：时钟回退检查 + 接受时刻 + 超时检测。
// 调用前必须已完成参数非法检查并持有锁。
func (c *Coordinator) begin(t Time) *Error {
	if c.clockStarted && t < c.lastTime {
		return clockRegressionf("time %d is before last accepted time %d", t, c.lastTime)
	}
	c.clockStarted = true
	c.lastTime = t
	c.enforceTimeouts(t)
	return nil
}

// enforceTimeouts 对所有有进行中快照的组做超时检测。
// 自动中止与手工中止等价，且发生在引起检测的那个操作的其余判定之前。
// 按组标识排序遍历，保证多个组同时超时时中止顺序确定。
func (c *Coordinator) enforceTimeouts(t Time) {
	if len(c.active) == 0 {
		return
	}
	ids := make([]string, 0, len(c.active))
	for id := range c.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g := c.active[id]
		switch g.phase {
		case PhaseFreezing:
			// 时刻严格大于冻结截止时刻仍未全部确认则自动中止；
			// 确认恰发生在截止时刻合法。
			if t > g.freezeDeadline {
				c.drain(g)
			}
		case PhaseFrozen:
			// 时刻严格大于 快照点+最长保持时长 仍未提交则自动中止；
			// 提交恰发生在边界时刻合法。
			if t > g.snapshotPoint+c.cfg.MaxHoldDuration {
				c.drain(g)
			}
		}
	}
}

// drain 结束组上进行中的快照：全部卷解冻，排队的写入按各卷到达次序
// 依次应用并分配写序号。提交与中止共用此路径；提交在调用 drain 前
// 已生成快照记录。
func (c *Coordinator) drain(g *group) {
	for _, id := range g.memberIDs {
		v := g.members[id]
		for range v.queue {
			v.seq++
		}
		v.queue = v.queue[:0]
		v.confirmed = false
	}
	g.phase = PhaseIdle
	g.remaining = 0
	delete(c.active, g.id)
}
