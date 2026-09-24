// Package norm 是变更日志规范化器：维护未了结序列与净值，按 fold 规则末尾抵消，所有读写加锁。
package norm

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"

	"ontology/fold"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrInvalidIncrement = errors.New("norm: increment must be a non-zero signed integer (magnitude k > 0)")
	ErrDepthExceeded    = errors.New("norm: changelog exceeds maxDepth")
	ErrNetOverflow      = errors.New("norm: net value overflows int64")
)

// Result 描述一次 Apply 的实际效果：Canceled=true 表示与末尾抵消，否则为追加。
type Result struct{ Canceled bool }

type Normalizer struct {
	mu         sync.RWMutex
	log        []int64
	net        int64
	maxDepth   int
	tailChecks int // 非导出：最近一次 Apply 检查序列末尾的次数
}

// New 创建长度上限为 maxDepth 的规范化器。
func New(maxDepth int) *Normalizer { return &Normalizer{maxDepth: maxDepth} }

// Apply 接收带符号增量（正增负撤，零非法）；拒绝全部发生在状态变更之前，失败不留痕。
func (n *Normalizer) Apply(op int64) (Result, error) {
	if op == 0 {
		return Result{}, ErrInvalidIncrement
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tailChecks = 0
	if len(n.log) > 0 {
		n.tailChecks++
		if tail := n.log[len(n.log)-1]; fold.Cancels(tail, op) {
			n.log, n.net = n.log[:len(n.log)-1], n.net-tail
			return Result{Canceled: true}, nil
		}
	}
	// 追加路径：先核验溢出与深度，任一失败状态不变。
	if op > 0 && n.net > math.MaxInt64-op || op < 0 && n.net < math.MinInt64-op {
		return Result{}, ErrNetOverflow
	}
	if len(n.log) >= n.maxDepth {
		return Result{}, ErrDepthExceeded
	}
	n.log, n.net = append(n.log, op), n.net+op
	return Result{}, nil
}

// Net 返回所有已接收操作的代数和。
func (n *Normalizer) Net() int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.net
}

// Changelog 返回未了结序列的副本，正负号即方向。
func (n *Normalizer) Changelog() []int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return slices.Clone(n.log)
}

// naiveForm 反复删除相邻反号等量对直到不动点（自由群规范形，与删除顺序无关）。
func naiveForm(ops []int64) []int64 {
	s := slices.Clone(ops)
	for {
		at := -1
		for i := 0; i+1 < len(s); i++ {
			if s[i] != 0 && s[i]+s[i+1] == 0 {
				at = i
				break
			}
		}
		if at < 0 {
			return s
		}
		s = append(s[:at], s[at+2:]...)
	}
}

func foldComplete(s []int64) bool {
	for i := 0; i+1 < len(s); i++ {
		if fold.Cancels(s[i], s[i+1]) {
			return false
		}
	}
	return true
}

func replaySum(s []int64) int64 {
	var sum int64
	for _, v := range s {
		sum += v
	}
	return sum
}

// SelfCheck 对内置操作序列核验四条不变量，并核验当前实例自身。
func (n *Normalizer) SelfCheck() error {
	n.mu.RLock()
	cur, net := slices.Clone(n.log), n.net
	n.mu.RUnlock()
	if !foldComplete(cur) || replaySum(cur) != net {
		return errors.New("selfcheck: current instance invariant mismatch")
	}
	eight := []int64{5, -5, -8, 8, 7, 2, -7, -2}
	z := New(8)
	for _, op := range eight {
		if _, err := z.Apply(op); err != nil {
			return err
		}
	}
	if got := z.Changelog(); !slices.Equal(got, []int64{7, 2, -7, -2}) || !slices.Equal(got, naiveForm(eight)) || z.Net() != 0 {
		return errors.New("selfcheck: eight-step mismatch")
	}
	rng := rand.New(rand.NewSource(1))
	for t := 0; t < 20; t++ {
		seq := make([]int64, 100)
		for i := range seq {
			v := int64(rng.Intn(3) + 1)
			if rng.Intn(2) == 0 {
				v = -v
			}
			seq[i] = v
		}
		m := New(256)
		for _, op := range seq {
			if _, err := m.Apply(op); err != nil {
				return err
			}
		}
		got := m.Changelog()
		if !slices.Equal(got, naiveForm(seq)) || !foldComplete(got) || replaySum(got) != m.Net() {
			return errors.New("selfcheck: random mismatch")
		}
	}
	return nil
}
