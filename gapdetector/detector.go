// Package gapdetector 提供按序号连续性的跳洞检测器。
//
// 检测器维护三个核心状态：
//   - 水位 watermark：已确认连续前缀的上界；
//   - 在途集 inflight：已到达但超前于水位的序号；
//   - 洞集 holes：已确认丢失、且一经判定绝不回撤的序号。
package gapdetector

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	// MinSeq 是合法序号下界。
	MinSeq = uint64(1)
	// MaxSeq 是合法序号上界，为窗口加法预留余量。
	MaxSeq = uint64(1)<<63 - 1
	// MaxWindow 是合法乱序窗口上界。
	MaxWindow = uint64(1)<<63 - 1
)

var (
	// ErrInvalidSeq 表示序号不在 [MinSeq, MaxSeq] 内。
	ErrInvalidSeq = errors.New("gapdetector: 非法序号")
	// ErrInvalidWindow 表示乱序窗口不在 [0, MaxWindow] 内。
	ErrInvalidWindow = errors.New("gapdetector: 非法乱序窗口")
	// ErrOverflow 表示 序号+窗口 超出 MaxSeq，存在溢出风险。
	ErrOverflow = errors.New("gapdetector: 序号加窗口溢出")
)

// DecisionKind 描述一次摄入的判定类别。
type DecisionKind int

const (
	// DecisionIgnored 表示序号不高于水位，被忽略（重复或迟到）。
	DecisionIgnored DecisionKind = iota
	// DecisionMerged 表示序号直接并入连续前缀。
	DecisionMerged
	// DecisionBuffered 表示序号超前于水位，进入在途集等待。
	DecisionBuffered
)

// Decision 是一次摄入的判定结果，包含可复现的判定依据。
type Decision struct {
	Kind DecisionKind
	Seq  uint64
	// Watermark 是本次摄入收敛后的水位。
	Watermark uint64
	// HolesDeclared 是本次收敛中新判出的洞（可能为空）。
	HolesDeclared []uint64
	// Reason 是人类可读的判定依据。
	Reason string
}

// Detector 是并发安全的跳洞检测器。
type Detector struct {
	mu          sync.Mutex
	window      uint64
	watermark   uint64
	inflight    map[uint64]struct{}
	holes       map[uint64]struct{}
	maxInflight uint64
}

// New 创建一个乱序窗口为 window 的检测器。
func New(window uint64) (*Detector, error) {
	if window > MaxWindow {
		return nil, fmt.Errorf("%w: window=%d 超过上界 %d", ErrInvalidWindow, window, MaxWindow)
	}
	return &Detector{
		window:   window,
		inflight: make(map[uint64]struct{}),
		holes:    make(map[uint64]struct{}),
	}, nil
}

// Ingest 摄入一个序号，返回判定结果；非法输入整体拒绝且不改变任何状态。
//
// 校验全部在加锁与状态变更之前完成，因此一次失败不会改变水位、在途集与洞集。
func (d *Detector) Ingest(seq uint64) (Decision, error) {
	if seq < MinSeq || seq > MaxSeq {
		return Decision{}, fmt.Errorf("%w: seq=%d 不在 [%d, %d] 内", ErrInvalidSeq, seq, MinSeq, MaxSeq)
	}
	if seq > MaxSeq-d.window {
		return Decision{}, fmt.Errorf("%w: seq=%d 加窗口 %d 超过 %d", ErrOverflow, seq, d.window, MaxSeq)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	dec := Decision{Seq: seq}
	if seq <= d.watermark {
		dec.Kind = DecisionIgnored
		dec.Watermark = d.watermark
		dec.Reason = fmt.Sprintf("seq=%d 不高于水位 %d，按重复/迟到忽略", seq, d.watermark)
		return dec, nil
	}
	if seq == d.watermark+1 {
		dec.Kind = DecisionMerged
		dec.Reason = fmt.Sprintf("seq=%d 等于水位+1，并入连续前缀", seq)
	} else {
		dec.Kind = DecisionBuffered
		dec.Reason = fmt.Sprintf("seq=%d 超前水位 %d，进入在途集等待", seq, d.watermark)
	}

	d.inflight[seq] = struct{}{}
	if seq > d.maxInflight {
		d.maxInflight = seq
	}
	d.convergeLocked(&dec)
	dec.Watermark = d.watermark
	return dec, nil
}

// convergeLocked 收敛水位，循环执行：
//   - 候选序号（水位+1）在在途集中：并入连续前缀；
//   - 否则若在途集最大值越过 候选序号+乱序窗口：把候选序号判为洞（绝不回撤）；
//   - 否则收敛完成。
func (d *Detector) convergeLocked(dec *Decision) {
	for len(d.inflight) > 0 {
		next := d.watermark + 1
		if _, ok := d.inflight[next]; ok {
			delete(d.inflight, next)
			d.watermark = next
			continue
		}
		if d.maxInflight <= next+d.window {
			return
		}
		d.holes[next] = struct{}{}
		dec.HolesDeclared = append(dec.HolesDeclared, next)
		d.watermark = next
	}
	d.maxInflight = 0
}

// Watermark 返回当前已确认连续前缀上界。
func (d *Detector) Watermark() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.watermark
}

// Inflight 返回在途集的有序快照。
func (d *Detector) Inflight() []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return sortedKeys(d.inflight)
}

// Holes 返回洞集的有序快照。
func (d *Detector) Holes() []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return sortedKeys(d.holes)
}

// Window 返回乱序窗口。
func (d *Detector) Window() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.window
}

// SelfCheck 校验内部不变量，全部满足时返回 nil。
//
// 不变量：
//  1. 在途集元素全部高于水位；
//  2. 洞集元素全部不高于水位；
//  3. 在途集与洞集不相交；
//  4. 在途集非空时，其最大值不超过 水位+1+窗口（候选序号+窗口）；
//  5. maxInflight 缓存与在途集实际最大值一致。
func (d *Detector) SelfCheck() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var actualMax uint64
	for seq := range d.inflight {
		if seq <= d.watermark {
			return fmt.Errorf("不变量1被破坏: 在途序号 %d 不高于水位 %d", seq, d.watermark)
		}
		if _, ok := d.holes[seq]; ok {
			return fmt.Errorf("不变量3被破坏: 序号 %d 同时在在途集与洞集中", seq)
		}
		if seq > actualMax {
			actualMax = seq
		}
	}
	for seq := range d.holes {
		if seq > d.watermark {
			return fmt.Errorf("不变量2被破坏: 洞 %d 高于水位 %d", seq, d.watermark)
		}
	}
	if len(d.inflight) > 0 && actualMax > d.watermark+1+d.window {
		return fmt.Errorf("不变量4被破坏: 在途最大值 %d 越过候选序号 %d 加窗口 %d", actualMax, d.watermark+1, d.window)
	}
	if actualMax != d.maxInflight {
		return fmt.Errorf("不变量5被破坏: maxInflight 缓存 %d 与实际最大值 %d 不一致", d.maxInflight, actualMax)
	}
	return nil
}

func sortedKeys(set map[uint64]struct{}) []uint64 {
	out := make([]uint64, 0, len(set))
	for seq := range set {
		out = append(out, seq)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
