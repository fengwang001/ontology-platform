package ontology

import (
	"fmt"
	"strings"
)

// naiveModel 是完全按题目规则逐步书写的独立朴素模拟，不与实现共享任何状态机代码。
type naiveModel struct {
	n, i, m, cp int
	pages       map[int]bool // page -> pf（comma-ok 区分「未缓存」与「pf=false」）
	lru         []int        // 最久未用 -> 最近使用
	prev        int
	ws, wsz     int
	mk          int
	hasMk       bool
	log         strings.Builder
}

func newNaive(n, i, m, cp int) *naiveModel {
	return &naiveModel{
		n: n, i: i, m: m, cp: cp,
		pages: map[int]bool{},
		prev:  -1,
	}
}

func (md *naiveModel) touch(p int) {
	for k, q := range md.lru {
		if q == p {
			md.lru = append(md.lru[:k], md.lru[k+1:]...)
			break
		}
	}
	md.lru = append(md.lru, p)
}

// step 返回需求读、预读、淘汰及错误码（0 正常 / 1 参数非法 / 2 越界）。
func (md *naiveModel) step(p, n int) ([]int, []int, []int, int) {
	fmt.Fprintf(&md.log, "Read(p=%d,n=%d) 输入；读前: prev=%d 窗口=(%d,%d) mk=%d(has=%v) LRU=%v pf=%v\n",
		p, n, md.prev, md.ws, md.wsz, md.mk, md.hasMk, md.lru, md.pages)

	if p < 0 || n < 1 {
		fmt.Fprintf(&md.log, "  判定：参数非法（p<0 或 n<1），拒绝且不改状态\n")
		return nil, nil, nil, 1
	}
	if p+n > md.n {
		fmt.Fprintf(&md.log, "  判定：越界（p+n=%d>N=%d），拒绝且不改状态\n", p+n, md.n)
		return nil, nil, nil, 2
	}

	seq := p == md.prev+1
	fmt.Fprintf(&md.log, "  判定：seq=%v（p==prev+1=%d）\n", seq, md.prev+1)

	missing := []int{}
	for q := p; q < p+n; q++ {
		if _, ok := md.pages[q]; !ok {
			missing = append(missing, q)
		}
	}

	ahead := []int{}
	if len(missing) > 0 {
		fmt.Fprintf(&md.log, "  判定：missing 非空=%v（同步路径）\n", missing)
		if seq {
			s := minInt(md.m, maxInt(md.i, maxInt(2*md.wsz, n)))
			rs := p + n
			if rs < md.n {
				for q := rs; q < minInt(rs+s, md.n); q++ {
					if _, ok := md.pages[q]; !ok {
						ahead = append(ahead, q)
					}
				}
				md.ws, md.wsz = rs, s
				md.mk, md.hasMk = rs+s/2, true
				fmt.Fprintf(&md.log, "  判定：顺序未命中 s=min(%d,max(%d,2*%d,%d))=%d rs=%d 预读=%v 窗口=(%d,%d) mk=%d（截断不改变 s）\n",
					md.m, md.i, md.wsz, n, s, rs, ahead, md.ws, md.wsz, md.mk)
			} else {
				md.ws, md.wsz, md.mk, md.hasMk = 0, 0, 0, false
				fmt.Fprintf(&md.log, "  判定：rs=%d>=N=%d，不预读，窗口清空\n", rs, md.n)
			}
		} else {
			md.ws, md.wsz, md.mk, md.hasMk = 0, 0, 0, false
			fmt.Fprintf(&md.log, "  判定：非顺序未命中，不预读，窗口清空\n")
		}
	} else {
		fmt.Fprintf(&md.log, "  判定：missing 为空（全命中，异步路径候选）\n")
		if md.wsz > 0 && md.hasMk && p <= md.mk && md.mk < p+n {
			s := minInt(md.m, maxInt(md.i, 2*md.wsz))
			rs := md.ws + md.wsz
			if rs < md.n {
				for q := rs; q < minInt(rs+s, md.n); q++ {
					if _, ok := md.pages[q]; !ok {
						ahead = append(ahead, q)
					}
				}
				md.ws, md.wsz = rs, s
				md.mk, md.hasMk = rs+s/2, true
				fmt.Fprintf(&md.log, "  判定：mk=%d 落在 [%d,%d) 内，异步 s=min(%d,max(%d,2*%d))=%d rs=%d 预读=%v 窗口=(%d,%d) mk=%d\n",
					md.mk, p, p+n, md.m, md.i, md.wsz, s, rs, ahead, md.ws, md.wsz, md.mk)
			} else {
				md.ws, md.wsz, md.mk, md.hasMk = 0, 0, 0, false
				fmt.Fprintf(&md.log, "  判定：异步 rs=%d>=N，窗口清空\n", rs)
			}
		} else {
			fmt.Fprintf(&md.log, "  判定：mk=%d 不在 [%d,%d) 或无窗口，不做任何事\n", md.mk, p, p+n)
		}
	}

	// 缓存更新：请求页升序（命中提升并清 pf；需求读插入 pf=false）。
	for q := p; q < p+n; q++ {
		md.touch(q)
		md.pages[q] = false
	}
	// 预读页升序插入，pf=true。
	for _, q := range ahead {
		md.touch(q)
		md.pages[q] = true
	}

	evicted := []int{}
	shrink := false
	for len(md.lru) > md.cp {
		victim := md.lru[0]
		md.lru = md.lru[1:]
		if md.pages[victim] {
			shrink = true
		}
		delete(md.pages, victim)
		evicted = append(evicted, victim)
	}
	if shrink && md.wsz > 0 {
		old := md.wsz
		md.wsz = maxInt(1, md.wsz/2)
		fmt.Fprintf(&md.log, "  判定：淘汰=%v 含未读 pf 且 wsz=%d>0，抖动收缩 %d->%d（ws/mk 不变）\n",
			evicted, old, old, md.wsz)
	} else {
		fmt.Fprintf(&md.log, "  判定：淘汰=%v（shrink=%v, wsz=%d）窗口保持\n", evicted, shrink, md.wsz)
	}

	md.prev = p + n - 1
	fmt.Fprintf(&md.log, "  输出：demand=%v ahead=%v evicted=%v；读后: prev=%d 窗口=(%d,%d) mk=%d LRU=%v\n",
		missing, ahead, evicted, md.prev, md.ws, md.wsz, md.mk, md.lru)
	return missing, ahead, evicted, 0
}

func (md *naiveModel) dropCache() {
	md.pages = map[int]bool{}
	md.lru = nil
	fmt.Fprintf(&md.log, "DropCache：清空缓存与 pf；保留 prev=%d 窗口=(%d,%d) mk=%d\n",
		md.prev, md.ws, md.wsz, md.mk)
}

func (md *naiveModel) snapshot() State {
	return State{
		Prev:   md.prev,
		Window: Window{Ws: md.ws, Wsz: md.wsz},
		Mk:     md.mk,
		HasMk:  md.hasMk,
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
