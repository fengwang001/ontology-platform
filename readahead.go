package ontology

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidArgument 表示 Read 的参数非法：p 为负或 n 小于 1。
	ErrInvalidArgument = errors.New("readahead: invalid argument")
	// ErrOutOfRange 表示读区间越界：p+n 大于文件总页数 N。
	ErrOutOfRange = errors.New("readahead: out of range")
)

// Window 描述当前预读窗口：起点 ws 与大小 wsz（wsz 为 0 表示无窗口）。
type Window struct {
	WS  int
	WSz int
}

// StateSnapshot 是 State 返回的状态快照。
type StateSnapshot struct {
	Prev   int
	Window Window
	// HasWindow 表示窗口是否存在；为 false 时 Mk 无意义。
	HasWindow bool
	Mk        int
}

// ReadAhead 是带有限页缓存与抖动回退的顺序预读窗口状态机。
// 所有方法可被并发调用，其结果等价于某个串行执行顺序。
type ReadAhead struct {
	mu sync.Mutex

	n  int // 文件总页数
	i  int // 初始窗口
	m  int // 最大窗口
	cp int // 缓存容量

	// cache 按最近使用次序保存已缓存页：最旧在前，最新在后。
	cache []int
	// pf 记录每个缓存页的「预读未读」标记。
	pf map[int]bool

	prev int // 上一次读的末页
	ws   int // 窗口起点
	wsz  int // 窗口大小，0 表示无窗口
	mk   int // 标记页（仅 wsz > 0 时有效）
}

// NewReadAhead 构造状态机。要求 N >= 1、I >= 1、M >= I、Cp >= 1，否则返回错误。
func NewReadAhead(totalPages, initialWindow, maxWindow, cacheCapacity int) (*ReadAhead, error) {
	if totalPages < 1 || initialWindow < 1 || maxWindow < initialWindow || cacheCapacity < 1 {
		return nil, ErrInvalidArgument
	}
	return &ReadAhead{
		n:    totalPages,
		i:    initialWindow,
		m:    maxWindow,
		cp:   cacheCapacity,
		pf:   make(map[int]bool),
		prev: -1,
	}, nil
}

// Read 读页 [p, p+n)，返回需求读页列表、预读页列表（各自升序）与被淘汰页列表（按淘汰次序）。
// 被拒绝的调用不会改变任何内部状态。
func (r *ReadAhead) Read(p, n int) (demand []int, readahead []int, evicted []int, err error) {
	if p < 0 || n < 1 {
		return nil, nil, nil, ErrInvalidArgument
	}
	if p+n > r.n {
		return nil, nil, nil, ErrOutOfRange
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	demand = []int{}
	readahead = []int{}
	evicted = []int{}

	end := p + n
	seq := p == r.prev+1

	missing := make(map[int]bool)
	for page := p; page < end; page++ {
		if !r.cachedLocked(page) {
			missing[page] = true
		}
	}

	newWindow := false
	newWS := 0
	newSize := 0

	if len(missing) > 0 {
		for page := p; page < end; page++ {
			if missing[page] {
				demand = append(demand, page)
			}
		}
		if seq {
			s := minInt(r.m, maxInt(r.i, maxInt(2*r.wsz, n)))
			rs := end
			if rs < r.n {
				newWindow = true
				newWS = rs
				newSize = s
			}
			// rs >= N：不预读且窗口清空（下面统一处理窗口更新）。
		}
		// 非顺序：不预读，窗口清空。
	} else if r.wsz > 0 && p <= r.mk && r.mk < end {
		// 全部命中且标记页落在请求区间内：异步预读。
		s := minInt(r.m, maxInt(r.i, 2*r.wsz))
		rs := r.ws + r.wsz
		if rs < r.n {
			newWindow = true
			newWS = rs
			newSize = s
		} else {
			// rs >= N：不预读，窗口清空。
			r.wsz = 0
		}
	}

	if newWindow {
		limit := minInt(newWS+newSize, r.n)
		for page := newWS; page < limit; page++ {
			if !r.cachedLocked(page) {
				readahead = append(readahead, page)
			}
		}
		r.ws = newWS
		r.wsz = newSize
		r.mk = newWS + newSize/2
	} else if len(missing) > 0 {
		// 同步分支：无论非顺序还是 rs >= N，窗口都清空。
		r.wsz = 0
	}
	// 全命中且未触发异步预读：窗口保持不变（若本来无窗口则仍无窗口）。

	// 缓存更新：先按升序处理请求页。
	for page := p; page < end; page++ {
		if missing[page] {
			// 需求读的页以最近使用插入，pf 为假。
			r.pf[page] = false
			r.cache = append(r.cache, page)
		} else {
			// 已缓存的页变为最近使用并清除 pf。
			r.touchLocked(page)
			r.pf[page] = false
		}
	}
	// 再按升序把预读页以最近使用插入，pf 为真。
	for _, page := range readahead {
		r.pf[page] = true
		r.cache = append(r.cache, page)
	}

	// 超出容量时从最久未使用者起逐页淘汰。
	evictedPF := false
	for len(r.cache) > r.cp {
		page := r.cache[0]
		r.cache = r.cache[1:]
		if r.pf[page] {
			evictedPF = true
		}
		delete(r.pf, page)
		evicted = append(evicted, page)
	}
	// 抖动回退：被淘汰页中至少一页 pf 为真，且此刻窗口存在。
	if evictedPF && r.wsz > 0 {
		r.wsz = maxInt(1, r.wsz/2)
	}

	r.prev = end - 1
	return demand, readahead, evicted, nil
}

// DropCache 清空已缓存页集合（连同 pf），但保留 prev、窗口与标记页。
func (r *ReadAhead) DropCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = nil
	r.pf = make(map[int]bool)
}

// State 返回 prev、窗口与标记页的快照。
func (r *ReadAhead) State() StateSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := StateSnapshot{Prev: r.prev, Window: Window{WS: r.ws, WSz: r.wsz}, HasWindow: r.wsz > 0}
	if r.wsz > 0 {
		snap.Mk = r.mk
	}
	return snap
}

func (r *ReadAhead) cachedLocked(page int) bool {
	_, ok := r.pf[page]
	return ok
}

// touchLocked 把已缓存页移到最近使用位置（切片末尾）。
func (r *ReadAhead) touchLocked(page int) {
	for idx, cached := range r.cache {
		if cached == page {
			r.cache = append(r.cache[:idx], r.cache[idx+1:]...)
			r.cache = append(r.cache, page)
			return
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
