// Package rsv 是单个蓄水池：持有容量 k 的槽位数组，并对第 i 个
// 到达的元素执行算法 R 的「填槽 / 替换槽 j / 丢弃」判定。
// 本包不依赖工程内任何其他包。
package rsv

import "errors"

// 哨兵错误：调用方用 errors.Is 判定。
var (
	// ErrBadCapacity：构造蓄水池时容量非正。
	ErrBadCapacity = errors.New("rsv: capacity k must be > 0")
	// ErrEmptyElement：到达元素为空串。
	ErrEmptyElement = errors.New("rsv: element must not be empty string")
	// ErrJOutOfRange：第 i 步（i>k）注入的随机数 j 不在 [1,i]。
	ErrJOutOfRange = errors.New("rsv: injected j out of range [1,i]")
)

// Reservoir 是容量固定为 k 的单个蓄水池。零值不可用，须经 New 构造。
type Reservoir struct {
	slots []string // 长度即已填槽位数（0..k），槽位编号 1..len(slots)
}

// New 构造容量 k（k>0）的空蓄水池。
func New(k int) (*Reservoir, error) {
	if k <= 0 {
		return nil, ErrBadCapacity
	}
	return &Reservoir{slots: make([]string, 0, k)}, nil
}

// Cap 返回蓄水池容量 k。
func (r *Reservoir) Cap() int { return cap(r.slots) }

// Len 返回当前已填槽位数，恒等于 min(k, 已喂入元素数)。
func (r *Reservoir) Len() int { return len(r.slots) }

// Offer 处理第 i 个（i 从 1 起）到达元素 e：
//   - i<=k：e 直接填入槽 i；
//   - i>k：j 必须落在 [1,i]，j<=k 则用 e 替换槽 j，否则丢弃 e。
//
// j 仅在 i>k 时被使用。任一分支校验失败都返回哨兵错误且不改动蓄水池。
func (r *Reservoir) Offer(i int, e string, j int) error {
	if e == "" {
		return ErrEmptyElement
	}
	switch {
	case i <= r.Cap():
		if i != len(r.slots)+1 { // 填槽必须严格按 1,2,...,k 衔接
			return ErrJOutOfRange
		}
		r.slots = append(r.slots, e)
		return nil
	default:
		if j < 1 || j > i {
			return ErrJOutOfRange
		}
		if j <= r.Cap() {
			r.slots[j-1] = e // 替换：槽位总数不变
		}
		return nil // j>k：丢弃，状态不变
	}
}

// Snapshot 返回槽 1..Len 内容的副本，未填槽不出现。
func (r *Reservoir) Snapshot() []string {
	out := make([]string, len(r.slots))
	copy(out, r.slots)
	return out
}

// Clone 返回蓄水池的深拷贝，供「整批成功后才提交」的影子执行使用。
func (r *Reservoir) Clone() *Reservoir {
	return &Reservoir{slots: append(make([]string, 0, r.Cap()), r.slots...)}
}
