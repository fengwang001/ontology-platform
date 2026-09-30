// Package auditor 实现流水线逐跳计数审计器：按事件时间桶汇总各阶段
// 乱序、重复上报的收发计数，并逐桶裁决平衡、丢失、重复、不守恒或缺报。
package auditor

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrStageOutOfRange 阶段编号越界。
	ErrStageOutOfRange = errors.New("阶段编号越界")
	// ErrNegativeBucket 桶号为负。
	ErrNegativeBucket = errors.New("桶号为负")
	// ErrNegativeCount 计数为负。
	ErrNegativeCount = errors.New("计数为负")
	// ErrNonPositiveSeq 序号非正。
	ErrNonPositiveSeq = errors.New("序号非正")
	// ErrStaleSeq 序号不大于已采用者。
	ErrStaleSeq = errors.New("序号过旧")
	// ErrWatermarkRegression 观察水位回退。
	ErrWatermarkRegression = errors.New("观察水位回退")
)

// Config 是审计器配置。
type Config struct {
	// Stages 为流水线阶段数 K，阶段编号 0..K-1。
	Stages int
	// Fanout 为各阶段展开倍数，长度必须为 Stages，每个值为正整数。
	Fanout []int64
	// BucketWidth 为事件时间桶宽（正数）。
	BucketWidth int64
	// Grace 为桶右端之后的宽限（非负）。
	Grace int64
}

func (c Config) validate() error {
	if c.Stages < 1 {
		return fmt.Errorf("auditor: 阶段数必须为正，得到 %d", c.Stages)
	}
	if len(c.Fanout) != c.Stages {
		return fmt.Errorf("auditor: 展开倍数长度 %d 与阶段数 %d 不一致", len(c.Fanout), c.Stages)
	}
	for i, f := range c.Fanout {
		if f < 1 {
			return fmt.Errorf("auditor: 阶段 %d 展开倍数必须为正，得到 %d", i, f)
		}
	}
	if c.BucketWidth < 1 {
		return fmt.Errorf("auditor: 桶宽必须为正，得到 %d", c.BucketWidth)
	}
	if c.Grace < 0 {
		return fmt.Errorf("auditor: 宽限不能为负，得到 %d", c.Grace)
	}
	return nil
}

// Report 是一条阶段上报：序号、收入、发出、丢弃（后三者为累计值）。
type Report struct {
	Stage  int
	Bucket int64
	Seq    int64
	In     int64
	Out    int64
	Drop   int64
}

type stageReport struct {
	adopted       bool
	seq           int64
	in, out, drop int64
}

type bucketState struct {
	reports       []stageReport
	reportedCount int
	settled       bool
	verdict       Verdict
}

// Auditor 是并发安全的逐跳计数审计器。
type Auditor struct {
	mu        sync.Mutex
	cfg       Config
	buckets   map[int64]*bucketState
	watermark int64
}

// New 创建审计器。
func New(cfg Config) (*Auditor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	fanout := make([]int64, len(cfg.Fanout))
	copy(fanout, cfg.Fanout)
	cfg.Fanout = fanout
	return &Auditor{cfg: cfg, buckets: make(map[int64]*bucketState)}, nil
}

// BucketOf 返回事件时间所属桶号：⌊事件时间÷桶宽⌋。
func (a *Auditor) BucketOf(eventTime int64) int64 {
	return eventTime / a.cfg.BucketWidth
}

// Report 处理一条上报；校验失败时整体拒绝且不改变任何状态。
// 校验顺序：阶段越界、桶号为负、计数为负、序号非正、序号过旧。
func (a *Auditor) Report(r Report) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reportLocked(r)
}

func (a *Auditor) reportLocked(r Report) error {
	if r.Stage < 0 || r.Stage >= a.cfg.Stages {
		return fmt.Errorf("%w: %d", ErrStageOutOfRange, r.Stage)
	}
	if r.Bucket < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeBucket, r.Bucket)
	}
	if r.In < 0 || r.Out < 0 || r.Drop < 0 {
		return fmt.Errorf("%w: in=%d out=%d drop=%d", ErrNegativeCount, r.In, r.Out, r.Drop)
	}
	if r.Seq <= 0 {
		return fmt.Errorf("%w: %d", ErrNonPositiveSeq, r.Seq)
	}
	b := a.buckets[r.Bucket]
	if b == nil {
		b = &bucketState{reports: make([]stageReport, a.cfg.Stages)}
		a.buckets[r.Bucket] = b
	}
	prev := b.reports[r.Stage]
	if prev.adopted && r.Seq <= prev.seq {
		return fmt.Errorf("%w: 阶段 %d 桶 %d 已采用序号 %d，拒绝序号 %d",
			ErrStaleSeq, r.Stage, r.Bucket, prev.seq, r.Seq)
	}
	if !prev.adopted {
		b.reportedCount++
	}
	b.reports[r.Stage] = stageReport{adopted: true, seq: r.Seq, in: r.In, out: r.Out, drop: r.Drop}
	a.refreshLocked(r.Bucket, b)
	return nil
}

// AdvanceWatermark 推进观察水位并结算到期的桶；回退整体拒绝。
func (a *Auditor) AdvanceWatermark(t int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < a.watermark {
		return fmt.Errorf("%w: 当前 %d，拒绝 %d", ErrWatermarkRegression, a.watermark, t)
	}
	a.watermark = t
	for id, b := range a.buckets {
		a.refreshLocked(id, b)
	}
	return nil
}

// Verdict 查询某桶当前裁决；未上报过的桶返回待定。
func (a *Auditor) Verdict(bucket int64) Verdict {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := a.buckets[bucket]
	if b == nil {
		return Verdict{Category: Pending, Stage: -1, Hop: -1, Reason: "桶不存在，待定"}
	}
	return b.verdict
}

// Watermark 返回当前观察水位。
func (a *Auditor) Watermark() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.watermark
}

// refreshLocked 在每次状态变化后检查结算条件并（重新）裁决。
func (a *Auditor) refreshLocked(bucket int64, b *bucketState) {
	if !b.settled {
		if b.reportedCount == a.cfg.Stages || a.watermark >= (bucket+1)*a.cfg.BucketWidth+a.cfg.Grace {
			b.settled = true
			b.verdict = a.judge(b)
			b.verdict.Settled = true
			b.verdict.Revision = 0
		}
		return
	}
	next := a.judge(b)
	next.Settled = true
	if next.equal(b.verdict) {
		next.Revision = b.verdict.Revision
	} else {
		next.Revision = b.verdict.Revision + 1
	}
	b.verdict = next
}

// judge 依据当前采用值，按阶段 0..K-1 顺序裁决，遇首个违规即返回。
func (a *Auditor) judge(b *bucketState) Verdict {
	k := a.cfg.Stages
	for s := 0; s < k; s++ {
		r := b.reports[s]
		if r.adopted && r.in*a.cfg.Fanout[s] != r.out+r.drop {
			return Verdict{
				Category: NonConservation, Stage: s, Hop: -1,
				Reason: fmt.Sprintf("阶段 %d 不守恒：收入 %d × 展开 %d = %d ≠ 发出 %d + 丢弃 %d = %d",
					s, r.in, a.cfg.Fanout[s], r.in*a.cfg.Fanout[s], r.out, r.drop, r.out+r.drop),
			}
		}
		if s+1 < k {
			next := b.reports[s+1]
			if r.adopted && next.adopted {
				if next.in < r.out {
					return Verdict{
						Category: Loss, Stage: -1, Hop: s, Delta: r.out - next.in,
						Reason: fmt.Sprintf("跳 %d→%d 丢失：阶段 %d 发出 %d > 阶段 %d 收入 %d，差额 %d",
							s, s+1, s, r.out, s+1, next.in, r.out-next.in),
					}
				}
				if next.in > r.out {
					return Verdict{
						Category: Duplicate, Stage: -1, Hop: s, Delta: next.in - r.out,
						Reason: fmt.Sprintf("跳 %d→%d 重复：阶段 %d 收入 %d > 阶段 %d 发出 %d，差额 %d",
							s, s+1, s+1, next.in, s, r.out, next.in-r.out),
					}
				}
			}
		}
	}
	for s := 0; s < k; s++ {
		if !b.reports[s].adopted {
			return Verdict{
				Category: MissingReport, Stage: s, Hop: -1,
				Reason: fmt.Sprintf("无违规但阶段 %d 从未上报，记缺报", s),
			}
		}
	}
	return Verdict{Category: Balanced, Stage: -1, Hop: -1, Reason: "守恒、逐跳平衡且全员上报，平衡"}
}
