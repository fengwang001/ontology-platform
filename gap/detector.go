// Package gap 提供按序号连续性的跳洞检测器。
//
// 检测器维护三部分状态：
//   - water：已确认连续前缀上界（水位）；
//   - inflight：已到达但相对当前水位超前的序号集合；
//   - holes：已确认丢失（不可回撤）的序号集合。
//
// 通过可配置的乱序窗口容忍轻微乱序：只有当在途集中的最远序号
// 超过某序号加上窗口时，该序号才会被判定为确认丢失。
package gap

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrIllegalSequence 表示摄入了非法序号（小于 1）。
var ErrIllegalSequence = errors.New("gap: illegal sequence number, must be >= 1")

// ErrIllegalWindow 表示构造检测器时使用了非法乱序窗口（小于 0）。
var ErrIllegalWindow = errors.New("gap: illegal reorder window, must be >= 0")

// ErrSequenceOverflow 表示序号超出检测器可处理的上界，继续处理会导致整数溢出。
var ErrSequenceOverflow = errors.New("gap: sequence number overflow")

// maxSequence 是检测器可接受的最大序号。
// 留出 1 的余量以保证窗口推进与内部 +1 运算不会溢出 int64。
const maxSequence = int64(1<<63-1) - 1

// Result 描述一次摄入的收敛结果。
type Result struct {
	// Seq 是本次摄入的序号。
	Seq int64
	// Water 是收敛后已确认连续前缀的上界。
	Water int64
	// Advanced 表示本次摄入是否推动了水位（前缀并入或洞确认）。
	Advanced bool
	// Duplicate 表示序号未高于水位，属于重复段并被忽略。
	Duplicate bool
	// NewHoles 是本次摄入收敛过程中新确认丢失的序号，按序号升序。
	NewHoles []int64
	// Reason 是判定依据的人类可读说明。
	Reason string
}

// Detector 是并发安全的跳洞检测器。
type Detector struct {
	mu       sync.RWMutex
	window   int64
	water    int64
	inflight map[int64]struct{}
	holes    map[int64]struct{}
}

// New 创建检测器。window 为允许的乱序窗口，必须非负。
func New(window int64) (*Detector, error) {
	if window < 0 {
		return nil, ErrIllegalWindow
	}
	return &Detector{
		window:   window,
		inflight: make(map[int64]struct{}),
		holes:    make(map[int64]struct{}),
	}, nil
}

// Ingest 摄入一个序号并执行收敛。
// 非法输入会被整体拒绝，且不改变任何内部状态。
func (d *Detector) Ingest(seq int64) (Result, error) {
	if seq < 1 {
		return Result{}, ErrIllegalSequence
	}
	if seq > maxSequence {
		return Result{}, ErrSequenceOverflow
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	result := Result{Seq: seq, Water: d.water}
	oldWater := d.water

	// 不高于水位的序号属于已确认前缀，重复段一律忽略且不产生任何判定。
	if seq <= d.water {
		result.Duplicate = true
		result.Reason = fmt.Sprintf(
			"seq=%d <= water=%d：已在确认连续前缀内，按重复段忽略，不判洞",
			seq, d.water)
		return result, nil
	}

	// 超前序号（含在途集中的重复序号）进入在途集后统一收敛。
	d.inflight[seq] = struct{}{}

	newHoles := make([]int64, 0)

	// 收敛循环：连续则并入前缀，缺口在被最远在途序号越过窗口后判为洞。
	for {
		next := d.water + 1
		maxInflight, ok := maxKey(d.inflight)
		if !ok {
			break
		}

		if _, arrived := d.inflight[next]; arrived {
			// next 已到达：它属于连续前缀，必须立即并入。
			// 缺口判洞交给后续轮次处理，绝不能跳过已到达的 next。
			delete(d.inflight, next)
			d.water = next
			continue
		}

		// next 缺失。最远在途序号尚未越过 next+window：
		// next 可能只是乱序迟到，继续等待，不做任何判定。
		if maxInflight <= next+d.window {
			break
		}

		// next 缺失，且最远在途序号已越过 next+window：
		// next 在乱序窗口内到达的可能性已不存在，确认为丢失洞，不可回撤。
		d.holes[next] = struct{}{}
		newHoles = append(newHoles, next)
		d.water = next
	}

	result.Water = d.water
	result.Advanced = d.water > oldWater
	result.NewHoles = newHoles
	result.Reason = buildReason(seq, d.water, d.window, newHoles)
	return result, nil
}

// Water 返回已确认连续前缀上界。单调不减。
func (d *Detector) Water() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.water
}

// Inflight 返回已到达但超前的在途序号快照，按序号升序。
func (d *Detector) Inflight() []int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return sortedKeys(d.inflight)
}

// Holes 返回已确认丢失的洞序号快照，按序号升序。洞集一旦确认不可回撤。
func (d *Detector) Holes() []int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return sortedKeys(d.holes)
}

// Check 对内部状态做自检，返回非 nil 错误表示不变量被破坏。
func (d *Detector) Check() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.window < 0 {
		return fmt.Errorf("window=%d 非法（负数）", d.window)
	}
	if d.water < 0 {
		return fmt.Errorf("water=%d 非法（负数）", d.water)
	}
	for seq := range d.inflight {
		if seq <= d.water {
			return fmt.Errorf("在途序号 %d 不高于水位 %d", seq, d.water)
		}
		if seq > maxSequence {
			return fmt.Errorf("在途序号 %d 越界", seq)
		}
		if _, isHole := d.holes[seq]; isHole {
			return fmt.Errorf("序号 %d 同时存在于在途集与洞集", seq)
		}
	}
	for seq := range d.holes {
		if seq <= 0 || seq > d.water {
			return fmt.Errorf("洞序号 %d 越界（应满足 1 <= hole <= water=%d）", seq, d.water)
		}
	}
	return nil
}

func maxKey(m map[int64]struct{}) (int64, bool) {
	for k := range m {
		max := k
		for kk := range m {
			if kk > max {
				max = kk
			}
		}
		return max, true
	}
	return 0, false
}

func sortedKeys(m map[int64]struct{}) []int64 {
	if len(m) == 0 {
		return []int64{}
	}
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func buildReason(seq, water, window int64, newHoles []int64) string {
	inflightHint := ""
	if len(newHoles) > 0 {
		inflightHint = fmt.Sprintf(
			"；收敛时最远在途序号越过对应缺口+window（window=%d），以下序号在乱序窗口内未到达，确认为洞且不回撤：%v",
			window, newHoles)
	}
	return fmt.Sprintf(
		"seq=%d 收敛后 water=%d，乱序窗口 window=%d%s",
		seq, water, window, inflightHint)
}
