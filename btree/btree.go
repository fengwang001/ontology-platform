// Package btree 实现一个按字节占用判定下溢的两层 B+ 树。
//
// 树只有叶页一层，叶页按键序排成页序。除最左页外，每页的分隔键
// 恒等于该页当前首键（每次操作完成后按此重算，不单独存储）。
package btree

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrInvalidArgument 构造参数越界、键为空串或记录尺寸越界。
	ErrInvalidArgument = errors.New("btree: invalid argument")
	// ErrKeyExists Insert 的键已存在。
	ErrKeyExists = errors.New("btree: key already exists")
	// ErrKeyNotFound Delete 的键不存在。
	ErrKeyNotFound = errors.New("btree: key not found")
	// ErrPageLimit Insert 需要切分而页数已等于上限。
	ErrPageLimit = errors.New("btree: page limit reached")
)

const (
	minCapacity = 8
	maxCapacity = 1_000_000
	maxPages    = 1_000_000
)

type record struct {
	key  string
	size int
}

type page struct {
	id   int
	recs []record
	occ  int
}

// PageView 是 Pages() 返回的页快照。
type PageView struct {
	ID        int
	Keys      []string
	Occupancy int
}

// Tree 是并发安全的两层 B+ 树。所有公开方法等价于某个串行顺序。
type Tree struct {
	mu      sync.Mutex
	cap     int // 页容量 C
	limit   int // 页数上限 P
	minOcc  int // M = ceil(C/2)
	maxRec  int // Emax = floor(C/4)
	pages   []*page
	nextID  int
	lastCmp int // 非导出计数器：最近一次定位页的分隔键比较次数
}

// New 构造一棵只有一个空页（编号 1）的树。
func New(capacity, pageLimit int) (*Tree, error) {
	if capacity < minCapacity || capacity > maxCapacity ||
		pageLimit < 1 || pageLimit > maxPages {
		return nil, fmt.Errorf("%w: capacity=%d pageLimit=%d", ErrInvalidArgument, capacity, pageLimit)
	}
	t := &Tree{
		cap:    capacity,
		limit:  pageLimit,
		minOcc: (capacity + 1) / 2,
		maxRec: capacity / 4,
		nextID: 1,
	}
	t.pages = []*page{{id: t.nextID}}
	t.nextID++
	return t, nil
}

// Insert 把 (key, size) 放入「分隔键不大于 key 的最靠右的页」，
// 没有这样的页则放入最左页；占用超过 C 时按字节差最小切分。
func (t *Tree) Insert(key string, size int) error {
	if key == "" || size < 1 || size > t.maxRec {
		return fmt.Errorf("%w: key=%q size=%d (Emax=%d)", ErrInvalidArgument, key, size, t.maxRec)
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	idx := t.locateLocked(key)
	p := t.pages[idx]
	pos := sort.Search(len(p.recs), func(i int) bool { return p.recs[i].key >= key })
	if pos < len(p.recs) && p.recs[pos].key == key {
		return fmt.Errorf("%w: %q", ErrKeyExists, key)
	}
	if p.occ+size > t.cap && len(t.pages) == t.limit {
		// 需要切分而页数已达上限：拒绝且不消耗页编号。
		return fmt.Errorf("%w: P=%d", ErrPageLimit, t.limit)
	}

	p.recs = append(p.recs, record{})
	copy(p.recs[pos+1:], p.recs[pos:])
	p.recs[pos] = record{key: key, size: size}
	p.occ += size
	if p.occ <= t.cap {
		return nil
	}
	t.splitLocked(idx)
	return nil
}

// splitLocked 把 pages[idx]（含新记录，n>=2 条）按左右占用差最小切分，
// 差相等取较小的 j；右半成为新页，取下一个编号，紧接在原页之后。
func (t *Tree) splitLocked(idx int) {
	p := t.pages[idx]
	n := len(p.recs)
	total := p.occ
	bestDiff, bestJ, left := -1, 1, 0
	for j := 1; j <= n-1; j++ {
		left += p.recs[j-1].size
		diff := total - 2*left
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			bestDiff, bestJ = diff, j
		}
	}
	np := &page{id: t.nextID}
	t.nextID++
	np.recs = append(np.recs, p.recs[bestJ:]...)
	for _, r := range np.recs {
		np.occ += r.size
	}
	p.recs = p.recs[:bestJ]
	p.occ = total - np.occ
	t.pages = append(t.pages, nil)
	copy(t.pages[idx+2:], t.pages[idx+1:])
	t.pages[idx+1] = np
}

// Delete 删除 key，下溢时按「左借、右借、并左、并右」处理一次，不级联。
func (t *Tree) Delete(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidArgument)
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	idx := t.locateLocked(key)
	p := t.pages[idx]
	pos := sort.Search(len(p.recs), func(i int) bool { return p.recs[i].key >= key })
	if pos >= len(p.recs) || p.recs[pos].key != key {
		return fmt.Errorf("%w: %q", ErrKeyNotFound, key)
	}
	size := p.recs[pos].size
	copy(p.recs[pos:], p.recs[pos+1:])
	p.recs = p.recs[:len(p.recs)-1]
	p.occ -= size
	if len(t.pages) == 1 || p.occ >= t.minOcc {
		return nil
	}
	t.rebalanceLocked(idx)
	return nil
}

// rebalanceLocked 对下溢页 U=pages[idx] 按固定次序处理一次，不级联：
// (1) 左借 (2) 右借 (3) 并左 (4) 并右；四步都不满足则保持下溢。
func (t *Tree) rebalanceLocked(idx int) {
	u := t.pages[idx]
	// (1) 左借 / (2) 右借
	if idx > 0 && t.borrowLocked(idx, idx-1, true) {
		return
	}
	if idx < len(t.pages)-1 && t.borrowLocked(idx, idx+1, false) {
		return
	}
	// (3) 并左：U 的记录并入 L 末尾，U 被释放。
	if idx > 0 {
		l := t.pages[idx-1]
		if l.occ+u.occ <= t.cap {
			l.recs = append(l.recs, u.recs...)
			l.occ += u.occ
			t.removePageLocked(idx)
			return
		}
	}
	// (4) 并右：R 的记录并入 U 末尾，R 被释放；存活的是页序中靠左的 U，编号不变。
	if idx < len(t.pages)-1 {
		r := t.pages[idx+1]
		if u.occ+r.occ <= t.cap {
			u.recs = append(u.recs, r.recs...)
			u.occ += r.occ
			t.removePageLocked(idx + 1)
			return
		}
	}
	// 四步都不满足：保持下溢，不算错误。
}

// borrowLocked 尝试从 donor=pages[di] 借记录给下溢页 U=pages[ui]。
// fromTail 为 true（左借）时从 donor 尾部起取，借来的记录按原序放到
// U 的开头；否则（右借）从 donor 头部起取，放到 U 的末尾。取最少的
// t（t>=1）条使 U 占用不小于 M，且取走后 donor 占用仍不小于 M；
// 满足则执行并返回 true，否则不改变任何页并返回 false。
func (t *Tree) borrowLocked(ui, di int, fromTail bool) bool {
	u, d := t.pages[ui], t.pages[di]
	sum := 0
	for take := 1; take <= len(d.recs); take++ {
		if fromTail {
			sum += d.recs[len(d.recs)-take].size
		} else {
			sum += d.recs[take-1].size
		}
		if u.occ+sum < t.minOcc {
			continue
		}
		if d.occ-sum < t.minOcc {
			return false // t 更大只会让 donor 更小，左/右借不可能
		}
		if fromTail {
			moved := append([]record(nil), d.recs[len(d.recs)-take:]...)
			u.recs = append(moved, u.recs...)
			d.recs = d.recs[:len(d.recs)-take]
		} else {
			u.recs = append(u.recs, d.recs[:take]...)
			d.recs = append([]record(nil), d.recs[take:]...)
		}
		u.occ += sum
		d.occ -= sum
		return true
	}
	return false
}

func (t *Tree) removePageLocked(idx int) {
	copy(t.pages[idx:], t.pages[idx+1:])
	t.pages[len(t.pages)-1] = nil
	t.pages = t.pages[:len(t.pages)-1]
}

// locateLocked 返回「分隔键不大于 key 的最靠右的页」的下标，
// 没有这样的页则返回 0（最左页）。分隔键即页首键，二分查找，
// 比较次数不超过 ceil(log2 m)（m 为页数，m=1 时为 0）。
func (t *Tree) locateLocked(key string) int {
	t.lastCmp = 0
	m := len(t.pages)
	idx := sort.Search(m-1, func(i int) bool {
		t.lastCmp++
		return t.pages[i+1].recs[0].key > key
	})
	return idx
}

// Pages 返回按页序排列的（编号、键列表、占用）拷贝。
func (t *Tree) Pages() []PageView {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]PageView, len(t.pages))
	for i, p := range t.pages {
		var keys []string
		if len(p.recs) > 0 {
			keys = make([]string, len(p.recs))
			for j, r := range p.recs {
				keys[j] = r.key
			}
		}
		out[i] = PageView{ID: p.id, Keys: keys, Occupancy: p.occ}
	}
	return out
}
