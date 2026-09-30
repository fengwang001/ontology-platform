// Package flowtree 维护带权重的流依赖树，并按树形结构把字节额度分给就绪的流。
//
// 虚拟根的编号恒为 0，它不参与任何分配。树中每个流（节点）在任意时刻
// 恰有一个父节点，且依赖关系从不成环。
package flowtree

import (
	"errors"
	"sync"
)

// 各操作的拒绝原因。返回的 error 与这些哨兵错误做 errors.Is 比较即可。
var (
	// ErrStreamUsed 开流：编号非正或已被使用过（存活或曾经存活）。
	ErrStreamUsed = errors.New("flowtree: stream id is non-positive or already used")
	// ErrParentMissing 开流：父节点不存在（不是 0 且不是存活流）。
	ErrParentMissing = errors.New("flowtree: parent stream does not exist")
	// ErrWeightRange 开流/重设：权重不在 [1, 256]。
	ErrWeightRange = errors.New("flowtree: weight out of range [1,256]")

	// ErrStreamMissing 重设/关闭：流不存在或已关闭。
	ErrStreamMissing = errors.New("flowtree: stream does not exist")
	// ErrNewParentMissing 重设：新父节点不存在。
	ErrNewParentMissing = errors.New("flowtree: new parent does not exist")
	// ErrParentIsSelf 重设：新父就是流自身。
	ErrParentIsSelf = errors.New("flowtree: new parent is the stream itself")

	// ErrReadyMissing 分配：就绪集合中存在不存在的流。
	ErrReadyMissing = errors.New("flowtree: ready stream does not exist")
	// ErrNegativeQuota 分配：额度为负。
	ErrNegativeQuota = errors.New("flowtree: quota is negative")
)

const (
	minWeight = 1
	maxWeight = 256
)

// node 是树中的一个流。虚拟根由 Allocator 单独维护，不放入 nodes。
type node struct {
	id       int
	parent   int
	weight   int
	children []int // 始终按编号升序，保证重放结果确定
}

// Allocator 是流依赖树与份额分配器。零值不可用，请用 New 创建。
// 所有方法均可被并发调用，内部用单一互斥锁串行化树变更与分配。
type Allocator struct {
	mu     sync.Mutex
	nodes  map[int]*node // 只含存活流；虚拟根 0 不在其中
	used   map[int]bool  // 所有曾经开通过的编号
	rootCh []int         // 虚拟根的子，按编号升序
}

// New 创建一个只含虚拟根 0 的空分配器。
func New() *Allocator {
	return &Allocator{
		nodes: make(map[int]*node),
		used:  make(map[int]bool),
	}
}
