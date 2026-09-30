// Package audit 实现流水线逐跳计数审计器。
//
// 审计器按事件时间桶汇总各阶段乱序、重复上报的收发计数，并逐桶裁决
// 平衡（balanced）、丢失（lost）、重复（duplicated）、不守恒
// （unbalanced）或缺报（missing）。迟到上报在桶结算后仍会被采纳并触发
// 重新裁决，其结果与“按最新上报重新推演”完全一致。
package audit

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ConfigError 描述审计器构造参数错误。
type ConfigError struct{ msg string }

func (e *ConfigError) Error() string { return "audit: " + e.msg }

// 可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	// ErrStageOutOfRange 阶段编号不在 [0, K-1]。
	ErrStageOutOfRange = errors.New("audit: stage out of range")
	// ErrNegativeBucket 桶号为负。
	ErrNegativeBucket = errors.New("audit: negative bucket")
	// ErrNegativeCount 收入、发出或丢弃计数为负。
	ErrNegativeCount = errors.New("audit: negative count")
	// ErrNonPositiveSeq 上报序号非正。
	ErrNonPositiveSeq = errors.New("audit: non-positive sequence number")
	// ErrStaleSeq 序号不大于该（阶段，桶）已采用的序号。
	ErrStaleSeq = errors.New("audit: stale sequence number")
	// ErrWatermarkRegression 观察水位较上一次回退。
	ErrWatermarkRegression = errors.New("audit: watermark regression")
)

// Category 是单个桶的裁决类别。
type Category int

const (
	// Pending 桶已存在但尚未结算。
	Pending Category = iota
	// Balanced 全部检查通过且无缺报。
	Balanced
	// Lost 某跳下游收入小于上游发出。
	Lost
	// Duplicated 某跳下游收入大于上游发出。
	Duplicated
	// Unbalanced 某阶段 收入*f_s != 发出+丢弃。
	Unbalanced
	// Missing 已结算但存在从未上报的阶段。
	Missing
)

// String 返回裁决类别的可读名称。
func (c Category) String() string {
	switch c {
	case Pending:
		return "pending"
	case Balanced:
		return "balanced"
	case Lost:
		return "lost"
	case Duplicated:
		return "duplicated"
	case Unbalanced:
		return "unbalanced"
	case Missing:
		return "missing"
	default:
		return fmt.Sprintf("category(%d)", int(c))
	}
}

// Report 是某阶段对某桶的一次上报，四个整数均为累计值。
type Report struct {
	Stage   int
	Bucket  int64
	Seq     int64
	Input   int64
	Output  int64
	Dropped int64
}

// Reading 是某（阶段，桶）当前被采用的上报内容。
type Reading struct {
	Seq     int64
	Input   int64
	Output  int64
	Dropped int64
}

// Verdict 是某个桶在某一时刻的唯一裁决。
type Verdict struct {
	// Category 裁决类别；结算前为 Pending。
	Category Category
	// Stage 是违规或缺报所在阶段：
	// Unbalanced/Missing 时为阶段编号，Lost/Duplicated 时为跳的上游阶段，
	// Pending/Balanced 时为 -1。
	Stage int
	// Diff 为跳裁决的差额：
	// Lost 时为 上游发出-下游收入（正数），
	// Duplicated 时为 下游收入-上游发出（正数），其余为 0。
	Diff int64
}

// BucketState 是查询单个桶得到的完整快照。
type BucketState struct {
	Bucket   int64
	Settled  bool
	Revision int
	Verdict  Verdict
	Readings map[int]Reading
}

// bucket 保存单个事件时间桶的全部可变状态。
type bucket struct {
	id       int64
	readings map[int]Reading
	settled  bool
	revision int
	verdict  Verdict
}

// Auditor 是并发安全的流水线逐跳计数审计器。
type Auditor struct {
	mu        sync.RWMutex
	stages    int
	width     int64
	grace     int64
	fanout    []int64
	watermark int64
	buckets   map[int64]*bucket
}

// New 创建审计器。stages 为阶段数 K，width 为桶宽（必须为正），
// grace 为结算宽限（可为 0），fanout 长度必须为 stages 且每个元素为正。
func New(stages int, width, grace int64, fanout []int64) (*Auditor, error) {
	if stages <= 0 {
		return nil, &ConfigError{msg: "stages must be positive"}
	}
	if width <= 0 {
		return nil, &ConfigError{msg: "bucket width must be positive"}
	}
	if grace < 0 {
		return nil, &ConfigError{msg: "grace must be non-negative"}
	}
	if len(fanout) != stages {
		return nil, &ConfigError{msg: fmt.Sprintf("fanout length %d != stages %d", len(fanout), stages)}
	}
	f := make([]int64, stages)
	for s, v := range fanout {
		if v <= 0 {
			return nil, &ConfigError{msg: fmt.Sprintf("fanout[%d] must be positive", s)}
		}
		f[s] = v
	}
	return &Auditor{
		stages:  stages,
		width:   width,
		grace:   grace,
		fanout:  f,
		buckets: make(map[int64]*bucket),
	}, nil
}

// validate 按题目规定的固定次序检查上报，只报第一个错误：
// 阶段越界 -> 桶号为负 -> 计数为负 -> 序号非正。
// 序号过旧需要读取已采用值，在持锁状态下另行检查。
func (r Report) validate(k int) error {
	if r.Stage < 0 || r.Stage >= k {
		return ErrStageOutOfRange
	}
	if r.Bucket < 0 {
		return ErrNegativeBucket
	}
	if r.Input < 0 || r.Output < 0 || r.Dropped < 0 {
		return ErrNegativeCount
	}
	if r.Seq <= 0 {
		return ErrNonPositiveSeq
	}
	return nil
}

// Report 采纳一次上报。被拒绝时返回对应错误且不改变任何状态。
// 返回该上报所属桶结算后的裁决；桶尚未结算时 Category 为 Pending。
func (a *Auditor) Report(r Report) (Verdict, error) {
	if err := r.validate(a.stages); err != nil {
		return Verdict{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	b := a.buckets[r.Bucket]
	if b != nil {
		if old, ok := b.readings[r.Stage]; ok && r.Seq <= old.Seq {
			return Verdict{}, ErrStaleSeq
		}
	}

	if b == nil {
		b = &bucket{
			id:       r.Bucket,
			readings: make(map[int]Reading),
			verdict:  Verdict{Category: Pending, Stage: -1},
		}
		a.buckets[r.Bucket] = b
	}
	prev := b.verdict
	b.readings[r.Stage] = Reading{
		Seq:     r.Seq,
		Input:   r.Input,
		Output:  r.Output,
		Dropped: r.Dropped,
	}

	if !b.settled && a.shouldSettle(b) {
		b.settled = true
		b.verdict = a.judge(b)
		// 结算时的首次裁决不计修订。
	} else if b.settled {
		// 已结算桶：每收到一条新上报都按最新采用值重新裁决；
		// 类别、位置或差额变化才计一次修订。
		nv := a.judge(b)
		if !verdictsEqual(nv, prev) {
			b.revision++
		}
		b.verdict = nv
	}
	return b.verdict, nil
}

// AdvanceWatermark 推进观察水位，单位与事件时间相同。
// 所有满足 水位 >= 桶右端+宽限 的已存在桶立即结算。
// 水位回退整体拒绝，不改变任何状态。
func (a *Auditor) AdvanceWatermark(w int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if w < a.watermark {
		return ErrWatermarkRegression
	}
	a.watermark = w

	// 水位推进可能同时结算多个桶，按桶号升序处理以保证重放确定性。
	ids := make([]int64, 0, len(a.buckets))
	for id := range a.buckets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		b := a.buckets[id]
		if !b.settled && a.shouldSettle(b) {
			b.settled = true
			b.verdict = a.judge(b)
		}
	}
	return nil
}

// Watermark 返回当前观察水位。
func (a *Auditor) Watermark() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.watermark
}

// Get 查询桶；桶尚未被任何上报创建时 ok 为 false。
func (a *Auditor) Get(bucket int64) (BucketState, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	b, ok := a.buckets[bucket]
	if !ok {
		return BucketState{}, false
	}
	return a.snapshot(b), true
}

// Buckets 按桶号升序返回全部已存在桶的快照。
func (a *Auditor) Buckets() []BucketState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]int64, 0, len(a.buckets))
	for id := range a.buckets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]BucketState, 0, len(ids))
	for _, id := range ids {
		out = append(out, a.snapshot(a.buckets[id]))
	}
	return out
}

// shouldSettle 报告两种结算触发（先到者为准）：
//  1. K 个阶段都已上报过该桶；
//  2. 观察水位 >= 桶右端 + 宽限，其中桶右端为 (桶号+1)*桶宽。
func (a *Auditor) shouldSettle(b *bucket) bool {
	if len(b.readings) >= a.stages {
		return true
	}
	rightEdge := (b.id + 1) * a.width
	return a.watermark >= rightEdge+a.grace
}

// judge 按阶段 0..K-1 依次检查并给出当前裁决。
// 每一步先查阶段自身守恒，再查该阶段到下一阶段的跳；
// 遇首个违规即返回，故缺报只会被更靠前的违规压过。
func (a *Auditor) judge(b *bucket) Verdict {
	for s := 0; s < a.stages; s++ {
		cur, curOK := b.readings[s]

		// 先查已上报阶段自身：收入*f_s == 发出+丢弃。
		if curOK && cur.Input*a.fanout[s] != cur.Output+cur.Dropped {
			return Verdict{Category: Unbalanced, Stage: s}
		}

		// 再查该阶段到下一阶段的跳；两端都已上报才比较。
		if s+1 < a.stages {
			if next, nextOK := b.readings[s+1]; curOK && nextOK {
				switch {
				case next.Input < cur.Output:
					return Verdict{Category: Lost, Stage: s, Diff: cur.Output - next.Input}
				case next.Input > cur.Output:
					return Verdict{Category: Duplicated, Stage: s, Diff: next.Input - cur.Output}
				}
			}
		}
	}

	// 无违规：存在从未上报的阶段则缺报，记编号最小者，否则平衡。
	for s := 0; s < a.stages; s++ {
		if _, ok := b.readings[s]; !ok {
			return Verdict{Category: Missing, Stage: s}
		}
	}
	return Verdict{Category: Balanced, Stage: -1}
}

func verdictsEqual(x, y Verdict) bool {
	return x.Category == y.Category && x.Stage == y.Stage && x.Diff == y.Diff
}

func (a *Auditor) snapshot(b *bucket) BucketState {
	rs := make(map[int]Reading, len(b.readings))
	for s, v := range b.readings {
		rs[s] = v
	}
	return BucketState{
		Bucket:   b.id,
		Settled:  b.settled,
		Revision: b.revision,
		Verdict:  b.verdict,
		Readings: rs,
	}
}

// 以下为内部占位，后续逐步填充。

var _ = sort.Ints
