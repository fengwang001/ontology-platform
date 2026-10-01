// Package ontology 实现顺序预读窗口状态机（见 README_READAHEAD.md）。
package ontology

import (
	"errors"
	"sync"
)

// ErrInvalidArg 表示 Read 参数非法（p<0 或 n<1）。
var ErrInvalidArg = errors.New("ontology: invalid read argument")

// ErrOutOfRange 表示读请求越过文件末尾（p+n>N）。
var ErrOutOfRange = errors.New("ontology: read range out of bounds")

// ErrInvalidConfig 表示构造参数不满足约束（N>=1、I>=1、M>=I、Cp>=1）。
var ErrInvalidConfig = errors.New("ontology: invalid configuration")

// Window 描述当前预读窗口：起点 Ws 与大小 Wsz（Wsz==0 表示无窗口）。
type Window struct {
	Ws  int
	Wsz int
}

// State 是状态机的只读快照。
type State struct {
	Prev   int
	Window Window
	Mk     int
	HasMk  bool
}

// Readahead 是带有限页缓存与抖动回退的顺序预读窗口状态机。
type Readahead struct {
	mu sync.Mutex
	n  int
	i  int
	m  int
	cp int
	// pages 记录已缓存页 -> pf 标记（true 表示「预读未读」）。
	pages map[int]bool
	// lru 按最久未用 -> 最近使用排列。
	lru   []int
	prev  int
	ws    int
	wsz   int
	mk    int
	hasMk bool
}

// NewReadahead 构造状态机；参数不合法时整体拒绝。
func NewReadahead(n, i, m, cp int) (*Readahead, error) {
	if n < 1 || i < 1 || m < i || cp < 1 {
		return nil, ErrInvalidConfig
	}
	return &Readahead{
		n:     n,
		i:     i,
		m:     m,
		cp:    cp,
		pages: make(map[int]bool),
		lru:   nil,
		prev:  -1,
		ws:    0,
		wsz:   0,
		mk:    0,
		hasMk: false,
	}, nil
}

// Read 处理一次对页区间 [p,p+n) 的读请求。
func (r *Readahead) Read(p, n int) ([]int, []int, []int, error) {
	// 校验先于加锁，且拒绝不改变任何状态。
	if p < 0 || n < 1 {
		return nil, nil, nil, ErrInvalidArg
	}
	if p+n > r.n {
		return nil, nil, nil, ErrOutOfRange
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	demand := []int{}
	ahead := []int{}

	// 以「读之前」的缓存与窗口为基准判定。
	seq := p == r.prev+1 // 首次读 p==0（prev==-1）自然满足。

	// (1)/(2) 依据读之前是否缺失选择同步预读或异步预读。
	syncMiss := false
	for q := p; q < p+n; q++ {
		if _, ok := r.pages[q]; !ok {
			syncMiss = true
			demand = append(demand, q)
		}
	}

	if syncMiss {
		if seq {
			s := r.m
			if cand := imax(r.i, imax(2*r.wsz, n)); cand < s {
				s = cand
			}
			rs := p + n
			if rs < r.n {
				for q := rs; q < rs+s && q < r.n; q++ {
					if _, ok := r.pages[q]; !ok {
						ahead = append(ahead, q)
					}
				}
				// 窗口大小不因截断到 N 而改变。
				r.ws, r.wsz = rs, s
				r.mk, r.hasMk = rs+s/2, true
			} else {
				// 预读起点越过文件末尾：不预读且窗口清空。
				r.ws, r.wsz, r.mk, r.hasMk = 0, 0, 0, false
			}
		} else {
			// 随机访问：不预读，窗口清空。
			r.ws, r.wsz, r.mk, r.hasMk = 0, 0, 0, false
		}
	} else if r.wsz > 0 && r.hasMk && p <= r.mk && r.mk < p+n {
		// 全命中且标记页落在请求区间内：异步预读。
		s := r.m
		if cand := imax(r.i, 2*r.wsz); cand < s {
			s = cand
		}
		rs := r.ws + r.wsz
		if rs < r.n {
			for q := rs; q < rs+s && q < r.n; q++ {
				if _, ok := r.pages[q]; !ok {
					ahead = append(ahead, q)
				}
			}
			r.ws, r.wsz = rs, s
			r.mk, r.hasMk = rs+s/2, true
		} else {
			r.ws, r.wsz, r.mk, r.hasMk = 0, 0, 0, false
		}
	}

	// (3) 缓存更新。
	// 请求页升序：命中页变最近使用并清 pf；需求读页以最近使用插入且 pf=false。
	for q := p; q < p+n; q++ {
		r.touch(q)
		r.pages[q] = false
	}
	// 预读页升序：以最近使用插入且 pf=true。
	for _, q := range ahead {
		r.touch(q)
		r.pages[q] = true
	}

	// 容量约束：从最久未使用者起淘汰。
	evicted := []int{}
	shrink := false
	for len(r.lru) > r.cp {
		victim := r.lru[0]
		r.lru = r.lru[1:]
		if r.pages[victim] {
			shrink = true
		}
		delete(r.pages, victim)
		evicted = append(evicted, victim)
	}
	// 抖动回退：被淘汰页中至少有一页 pf=true，且此刻仍有窗口。
	if shrink && r.wsz > 0 {
		r.wsz = shrinkSize(r.wsz)
	}

	// (4) 记录本次读的末页。
	r.prev = p + n - 1

	return demand, ahead, evicted, nil
}

// DropCache 清空已缓存页集合（连同 pf 标记），保留 prev、窗口与标记页。
func (r *Readahead) DropCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pages = make(map[int]bool)
	r.lru = nil
}

// State 返回 prev、窗口与标记页的快照。
func (r *Readahead) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return State{
		Prev:   r.prev,
		Window: Window{Ws: r.ws, Wsz: r.wsz},
		Mk:     r.mk,
		HasMk:  r.hasMk,
	}
}

// touch 将页移动为最近使用（不在缓存则追加）。
func (r *Readahead) touch(page int) {
	for idx, q := range r.lru {
		if q == page {
			r.lru = append(r.lru[:idx], r.lru[idx+1:]...)
			break
		}
	}
	r.lru = append(r.lru, page)
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// shrinkSize 是抖动回退的窗口收缩函数：max(1, floor(wsz/2))。
func shrinkSize(wsz int) int {
	if half := wsz / 2; half > 1 {
		return half
	}
	return 1
}
