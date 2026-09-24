// Package norm 维护未了结序列（changelog）、推进净值并执行折叠。
// 依赖 fold；不依赖 api。
package norm

import (
	"errors"
	"math"
	"sync"

	"ontology/fold"
)

// 三类可判定故障，互不相同的哨兵错误。
var (
	ErrInvalidDelta  = errors.New("norm: 增量非法（k 必须为正整数且量值可表示）")
	ErrDepthExceeded = errors.New("norm: 深度超限（changelog 长度超过 maxDepth）")
	ErrNetOverflow   = errors.New("norm: 净值溢出（Net 超出 int64 可表示范围）")
)

// Op 是一条带符号增量：+k 增加 k，-k 撤回 k，k 为正整数。
type Op = int64

// Normalizer 是规范化器。零值不可用，须用 New 构造。
type Normalizer struct {
	mu       sync.RWMutex
	maxDepth int
	seq      []int64 // 未了结序列，正负号即方向
	net      int64
	checks   int // 非导出：最近一次 Apply 检查序列末尾的次数
}

// New 构造一个 changelog 长度上限为 maxDepth 的规范化器。
func New(maxDepth int) *Normalizer {
	if maxDepth < 0 {
		maxDepth = 0
	}
	return &Normalizer{maxDepth: maxDepth}
}

// Apply 接收一条操作：与末尾抵消则弹出末尾，否则追加。
// 任何校验失败都整体失败、状态不变，并返回可判定哨兵错误。
func (n *Normalizer) Apply(op Op) error {
	if op == 0 || op == math.MinInt64 { // k<=0 非法；MinInt64 量值不可表示
		return ErrInvalidDelta
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.checks = 0
	cancel := false
	if len(n.seq) > 0 {
		n.checks++ // 只检查末尾一条，O(1)
		cancel = fold.Cancels(n.seq[len(n.seq)-1], op)
	}
	if !cancel && len(n.seq)+1 > n.maxDepth {
		return ErrDepthExceeded
	}
	// 抵消与追加都可能使净值溢出（Net=所有已接收操作的代数和），统一校验。
	if (op > 0 && n.net > math.MaxInt64-op) || (op < 0 && n.net < math.MinInt64-op) {
		return ErrNetOverflow
	}
	if cancel {
		// 抵消：弹出末尾、本操作不入列；net+=op 等价于减去 tail 的贡献。
		n.seq = n.seq[:len(n.seq)-1]
	} else {
		n.seq = append(n.seq, op)
	}
	n.net += op
	return nil
}

// Net 返回净值：所有已接收操作的代数和，等于重放 changelog 的结果。
func (n *Normalizer) Net() int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.net
}

// Changelog 返回未了结序列的副本，调用方可安全修改返回值。
func (n *Normalizer) Changelog() []int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]int64, len(n.seq))
	copy(out, n.seq)
	return out
}
