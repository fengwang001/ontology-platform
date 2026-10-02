package tablespace

import "sort"

// naiveModel 是严格按题面规则逐步直写的朴素参考模型：
// 不做任何索引或惰性优化，每次选择都线性扫描，便于与实现交叉验证。
type naiveModel struct {
	x, f, e int

	state []string // FREE / FRAG / FULLFRAG / SEG
	owner []int    // SEG 状态的独占段号
	used  []int    // 区段已用页数
	page  [][]int  // page[ext][off] 归属段号（碎片与独占统一记录，0 为空闲）

	alive  []bool  // alive[s]
	sused  []int   // sused[s]
	queue  [][]int // queue[s]：s 的非满独占区段（按加入次序）
	nextID int
}

func newNaive(x, f, e int) *naiveModel {
	m := &naiveModel{
		x: x, f: f, e: e,
		state:  make([]string, e),
		owner:  make([]int, e),
		used:   make([]int, e),
		page:   make([][]int, e),
		alive:  []bool{false},
		sused:  []int{0},
		queue:  [][]int{{}},
		nextID: 1,
	}
	for i := 0; i < e; i++ {
		m.state[i] = "FREE"
		m.page[i] = make([]int, x)
	}
	return m
}

func (m *naiveModel) newSegment() int {
	id := m.nextID
	m.nextID++
	m.alive = append(m.alive, true)
	m.sused = append(m.sused, 0)
	m.queue = append(m.queue, nil)
	return id
}

func (m *naiveModel) allocPage(s, hint int) (int, string, bool) {
	if hint < -1 || hint >= m.e*m.x {
		return -1, "INVALID_ARGUMENT", false
	}
	if s <= 0 || s >= len(m.alive) || !m.alive[s] {
		return -1, "NO_SUCH_SEGMENT", false
	}
	if m.sused[s] < m.f {
		return m.allocFrag(s)
	}
	return m.allocSeg(s, hint)
}

func (m *naiveModel) allocFrag(s int) (int, string, bool) {
	idx := -1
	for i := 0; i < m.e; i++ {
		if m.state[i] == "FRAG" {
			idx = i
			break
		}
	}
	if idx == -1 {
		for i := 0; i < m.e; i++ {
			if m.state[i] == "FREE" {
				idx = i
				break
			}
		}
		if idx == -1 {
			return -1, "NO_SPACE", false
		}
		m.state[idx] = "FRAG"
	}
	off := -1
	for k := 0; k < m.x; k++ {
		if m.page[idx][k] == 0 {
			off = k
			break
		}
	}
	p := idx*m.x + off
	m.page[idx][off] = s
	m.used[idx]++
	m.sused[s]++
	if m.used[idx] == m.x {
		m.state[idx] = "FULLFRAG"
	}
	return p, "OK:frag", true
}

func (m *naiveModel) allocSeg(s, hint int) (int, string, bool) {
	if hint >= 0 {
		idx, off := hint/m.x, hint%m.x
		if m.state[idx] == "SEG" && m.owner[idx] == s && m.page[idx][off] == 0 {
			m.assignSeg(s, idx, off)
			return hint, "OK:seg-hint", true
		}
	}
	if len(m.queue[s]) > 0 {
		idx := m.queue[s][0]
		off := -1
		for k := 0; k < m.x; k++ {
			if m.page[idx][k] == 0 {
				off = k
				break
			}
		}
		m.assignSeg(s, idx, off)
		return idx*m.x + off, "OK:seg-queue", true
	}
	idx := -1
	for i := 0; i < m.e; i++ {
		if m.state[i] == "FREE" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return -1, "NO_SPACE", false
	}
	m.state[idx] = "SEG"
	m.owner[idx] = s
	m.queue[s] = append(m.queue[s], idx)
	m.assignSeg(s, idx, 0)
	return idx * m.x, "OK:seg-new", true
}

func (m *naiveModel) assignSeg(s, idx, off int) {
	m.page[idx][off] = s
	m.used[idx]++
	m.sused[s]++
	if m.used[idx] == m.x {
		// 用满移出队列（必在队中：hint 与最小空闲页只取自未用满区段）。
		q := m.queue[s]
		for i, v := range q {
			if v == idx {
				m.queue[s] = append(q[:i], q[i+1:]...)
				break
			}
		}
	}
}

func (m *naiveModel) freePage(s, p int) (string, bool) {
	if p < 0 || p >= m.e*m.x {
		return "INVALID_ARGUMENT", false
	}
	if s <= 0 || s >= len(m.alive) || !m.alive[s] {
		return "NO_SUCH_SEGMENT", false
	}
	idx, off := p/m.x, p%m.x
	switch m.state[idx] {
	case "FRAG", "FULLFRAG":
		if m.page[idx][off] != s {
			return "PAGE_NOT_OWNED", false
		}
	case "SEG":
		if m.owner[idx] != s {
			return "PAGE_NOT_OWNED", false
		}
		if m.page[idx][off] == 0 {
			return "PAGE_NOT_OWNED", false
		}
	default:
		return "PAGE_NOT_OWNED", false
	}
	m.page[idx][off] = 0
	m.used[idx]--
	m.sused[s]--
	if m.state[idx] == "FRAG" || m.state[idx] == "FULLFRAG" {
		if m.state[idx] == "FULLFRAG" {
			m.state[idx] = "FRAG"
		}
		if m.used[idx] == 0 {
			m.state[idx] = "FREE"
		}
		return "OK:frag-free", true
	}
	// SEG
	wasFull := m.used[idx]+1 == m.x
	if wasFull {
		m.queue[s] = append(m.queue[s], idx)
	}
	if m.used[idx] == 0 {
		m.state[idx] = "FREE"
		m.owner[idx] = 0
		q := m.queue[s]
		for i, v := range q {
			if v == idx {
				m.queue[s] = append(q[:i], q[i+1:]...)
				break
			}
		}
	}
	return "OK:seg-free", true
}

// freeSegment 等价于逐页释放，但页内/区段顺序固定为可复现的次序。
func (m *naiveModel) freeSegment(s int) (string, bool) {
	if s <= 0 || s >= len(m.alive) || !m.alive[s] {
		return "NO_SUCH_SEGMENT", false
	}
	// 收集所有归 s 的页。
	var pages []int
	for idx := 0; idx < m.e; idx++ {
		for off := 0; off < m.x; off++ {
			if (m.state[idx] == "FRAG" || m.state[idx] == "FULLFRAG") && m.page[idx][off] == s {
				pages = append(pages, idx*m.x+off)
			}
		}
	}
	sort.Ints(pages)
	for _, p := range pages {
		m.freePage(s, p)
	}
	// 独占区段：此时其中页可能仍在（整区页不经 page 标记？——本模型统一记录在 page 中，
	// 故上面只收了碎片页）。再收集 SEG(s) 区段内全部页，按区段、页号升序释放。
	var segPages []int
	for idx := 0; idx < m.e; idx++ {
		if m.state[idx] == "SEG" && m.owner[idx] == s {
			for off := 0; off < m.x; off++ {
				if m.page[idx][off] == s {
					segPages = append(segPages, idx*m.x+off)
				}
			}
		}
	}
	sort.Ints(segPages)
	for _, p := range segPages {
		m.freePage(s, p)
	}
	m.alive[s] = false
	m.queue[s] = nil
	m.sused[s] = 0
	return "OK:segment", true
}
