// Package rsv 实现单个蓄水池：槽位数组、容量 k、第 i 步的替换/丢弃判定。
package rsv

import "errors"

var (
	// ErrBadCapacity 表示容量 k <= 0。
	ErrBadCapacity = errors.New("rsv: capacity must be positive")
	// ErrBadDraw 表示第 i 步的随机数 j 越出 [1, i]。
	ErrBadDraw = errors.New("rsv: draw out of range [1,i]")
)

// Reservoir 是容量为 k 的单个蓄水池，槽位编号 1..k。
type Reservoir struct {
	slots  []string
	filled int
}

// New 创建容量为 k 的蓄水池。
func New(k int) (*Reservoir, error) {
	if k <= 0 {
		return nil, ErrBadCapacity
	}
	return &Reservoir{slots: make([]string, k)}, nil
}

// Step 处理第 i 个到达的元素 e（i 从 1 起）；i>k 时 j 为该步抽取的随机数。
func (r *Reservoir) Step(i int, e string, j int) error {
	if i <= len(r.slots) {
		r.slots[i-1] = e // 前 k 个直接填槽 i，保序
		r.filled = i
		return nil
	}
	if j < 1 || j > i {
		return ErrBadDraw
	}
	if j <= len(r.slots) {
		r.slots[j-1] = e // j<=k 替换槽 j，否则丢弃
	}
	return nil
}

// Slots 返回已填槽位的副本（槽 1..filled 顺序）。
func (r *Reservoir) Slots() []string {
	out := make([]string, r.filled)
	copy(out, r.slots[:r.filled])
	return out
}

// K 返回容量 k。
func (r *Reservoir) K() int { return len(r.slots) }
