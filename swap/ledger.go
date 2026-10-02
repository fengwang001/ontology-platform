// Package swap 实现交换区槽位账本：为换出的页分配槽位，
// 以引用计数（count）与交换缓存标志（cache）共同决定槽位何时回收，
// 并按「簇优先、下一适应（next-fit）、整簇空闲队列」的次序分配槽位。
//
// 槽位编号 0 到 N-1，其中 0 号为交换区头部不可用，可用槽位为 1 到 N-1。
// 第 c 簇是编号落在 [c*K, (c+1)*K) 内的可用槽位；没有可用槽位的簇不存在，
// 首簇与末簇可能不足 K 个。槽位空闲当且仅当 count 为 0 且 cache 为假。
//
// 分配次序（第一个可行的生效）：
//  1. cur 非空且 cur 簇内存在编号不小于 cursor 的空闲槽位：取其中最小者 s；
//  2. 旧 cur 若整簇空闲则追加到队尾，cur 置空，取队首簇为 cur，
//     取其最小编号可用槽位 s；
//  3. 队列为空时取全部空闲槽位中编号最小者 s，cur 置为 s 所在簇。
//
// 三种情况都将 cursor 置为 s+1。游标之前空出的槽位在 cur 内不会被重用，
// 只有走到第 3 步才可能被取到。
//
// 整簇空闲队列保存「整簇空闲且不是 cur」的簇，先进先出：
// 一个簇在该条件成立的那一刻追加到队尾（槽位回收使其整簇空闲、
// 或 Alloc 第 2 步替换 cur 时旧 cur 整簇空闲），在成为 cur 时离队。
//
// 所有导出方法都可在并发下调用，效果等价于某个串行顺序；
// Fork/Release 是整批原子的：先按次序在副本上推演，遇到第一个失败的
// 元素就返回（下标, 错误）且整批不生效。
//
// 性能：Alloc 非回退路径考察的槽位数不超过 K（由 probes 计数）；
// 回退路径借助按簇空闲计数的线段树，考察的簇数不超过
// 2*ceil(log2(簇数))+2；Free、CacheDrop 与队列维护为 O(1) 摊还。
package swap

import (
	"errors"
	"sync"
)

var (
	// ErrConfig 表示构造参数越界（N、K 或 Max 不在允许范围内）。
	ErrConfig = errors.New("swap: invalid configuration")
	// ErrNoSpace 表示全部可用槽位都在用，Alloc 无法分配。
	ErrNoSpace = errors.New("swap: no free slot")
	// ErrRange 表示槽位编号不在 1 到 N-1。
	ErrRange = errors.New("swap: slot out of range")
	// ErrNotInUse 表示槽位空闲（count 为 0 且 cache 为假）。
	ErrNotInUse = errors.New("swap: slot not in use")
	// ErrOverflow 表示 Dup 时 count 已等于 Max。
	ErrOverflow = errors.New("swap: refcount overflow")
	// ErrUnderflow 表示 Free 时 count 已为 0（仅由缓存持有）。
	ErrUnderflow = errors.New("swap: refcount underflow")
	// ErrExists 表示 CacheAdd 时 cache 已为真。
	ErrExists = errors.New("swap: cache flag already set")
	// ErrNoCache 表示 CacheDrop 时 cache 已为假。
	ErrNoCache = errors.New("swap: cache flag not set")
)

const (
	maxN        = 1_000_000
	maxK        = 1024
	maxRefCount = 255
)

// Ledger 是交换区槽位账本。零值不可用，须用 New 构造。
type Ledger struct {
	mu sync.Mutex

	n        int
	k        int
	maxCount int

	count []uint8 // 每个槽位的引用计数，0 到 maxCount
	cache []bool  // 每个槽位的交换缓存标志

	cMin          int   // 最小簇编号（K=1 时第 0 簇不存在）
	cMax          int   // 最大簇编号
	clusterUsable []int // 每簇可用槽位数，下标为簇编号
	clusterFree   []int // 每簇空闲槽位数，下标为簇编号

	seg     []int // 线段树：区间内有空闲槽位的最小簇编号，无则为 segInf
	segSize int
	segInf  int

	cur    int // 当前簇，-1 表示为空
	cursor int // 下一适应游标

	queue []int // 整簇空闲队列（qHead 起为有效部分）
	qHead int

	used int // 在用槽位数

	probes int // 最近一次 Alloc 考察的槽位数（第 1 步）或簇数（第 3 步）
}

// New 构造槽位总数为 n、簇大小为 k、单槽引用计数上限为 maxCount 的账本。
// 任一参数越界（2<=n<=1e6，1<=k<=1024，1<=maxCount<=255）则整体拒绝。
func New(n, k, maxCount int) (*Ledger, error) {
	if n < 2 || n > maxN || k < 1 || k > maxK || maxCount < 1 || maxCount > maxRefCount {
		return nil, ErrConfig
	}
	l := &Ledger{
		n:        n,
		k:        k,
		maxCount: maxCount,
		count:    make([]uint8, n),
		cache:    make([]bool, n),
		cur:      -1,
	}
	l.cMax = (n - 1) / k
	l.cMin = 0
	if l.usable(0) == 0 {
		l.cMin = 1
	}
	l.clusterUsable = make([]int, l.cMax+1)
	l.clusterFree = make([]int, l.cMax+1)
	for c := 0; c <= l.cMax; c++ {
		u := l.usable(c)
		l.clusterUsable[c] = u
		l.clusterFree[c] = u
	}
	l.segSize = 1
	for l.segSize < l.cMax+1 {
		l.segSize <<= 1
	}
	l.segInf = l.cMax + 1
	l.seg = make([]int, 2*l.segSize)
	for i := range l.seg {
		l.seg[i] = l.segInf
	}
	for c := 0; c <= l.cMax; c++ {
		if l.clusterFree[c] > 0 {
			l.seg[l.segSize+c] = c
		}
	}
	for i := l.segSize - 1; i >= 1; i-- {
		l.seg[i] = min(l.seg[2*i], l.seg[2*i+1])
	}
	for c := l.cMin; c <= l.cMax; c++ {
		l.queue = append(l.queue, c)
	}
	return l, nil
}

// usable 返回第 c 簇的可用槽位数。
func (l *Ledger) usable(c int) int {
	lo := max(c*l.k, 1)
	hi := min((c+1)*l.k-1, l.n-1)
	if hi < lo {
		return 0
	}
	return hi - lo + 1
}

// firstSlot 返回第 c 簇编号最小的可用槽位。
func (l *Ledger) firstSlot(c int) int {
	return max(c*l.k, 1)
}

// freeSlot 报告槽位 s 是否空闲。
func (l *Ledger) freeSlot(s int) bool {
	return l.count[s] == 0 && !l.cache[s]
}

// scanFree 在第 c 簇内找编号不小于 from 的最小空闲槽位，无则返回 -1。
// countProbes 为真时把考察的槽位计入 probes（Alloc 第 1 步）。
func (l *Ledger) scanFree(c, from int, countProbes bool) int {
	lo := max(l.firstSlot(c), from)
	hi := min((c+1)*l.k-1, l.n-1)
	for s := lo; s <= hi; s++ {
		if countProbes {
			l.probes++
		}
		if l.freeSlot(s) {
			return s
		}
	}
	return -1
}

// segUpdate 在第 c 簇空闲计数变化后更新线段树。
func (l *Ledger) segUpdate(c int) {
	i := l.segSize + c
	if l.clusterFree[c] > 0 {
		l.seg[i] = c
	} else {
		l.seg[i] = l.segInf
	}
	for i > 1 {
		i >>= 1
		l.seg[i] = min(l.seg[2*i], l.seg[2*i+1])
	}
}

// pushQueue 把簇 c 追加到整簇空闲队列队尾。
func (l *Ledger) pushQueue(c int) {
	l.queue = append(l.queue, c)
}

// popQueue 取出整簇空闲队列队首。
func (l *Ledger) popQueue() int {
	c := l.queue[l.qHead]
	l.qHead++
	if l.qHead >= 64 && l.qHead*2 >= len(l.queue) {
		l.queue = l.queue[:copy(l.queue, l.queue[l.qHead:])]
		l.qHead = 0
	}
	return c
}

// makeFree 把槽位 s 标记为空闲并维护簇计数、线段树与整簇空闲队列。
func (l *Ledger) makeFree(s int) {
	c := s / l.k
	l.clusterFree[c]++
	l.segUpdate(c)
	l.used--
	if l.clusterFree[c] == l.clusterUsable[c] && c != l.cur {
		l.pushQueue(c)
	}
}
