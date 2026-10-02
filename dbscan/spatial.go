package dbscan

import "sort"

const (
	maxCoord    int64 = 1_000_000
	maxTick     int64 = 1_000_000_000_000_000
	maxEps      int64 = 1_000_000
	maxW        int64 = 1_000_000_000
	maxMinPts         = 1000
	maxCapacity       = 100000
)

// New 用给定参数构造服务。
func New(eps int64, minPts int, w int64, c int) (*Service, error) {
	if eps < 1 || eps > maxEps {
		return nil, invalidParam("eps must be in [1,1e6]")
	}
	if minPts < 1 || minPts > maxMinPts {
		return nil, invalidParam("minPts must be in [1,1000]")
	}
	if w < 1 || w > maxW {
		return nil, invalidParam("W must be in [1,1e9]")
	}
	if c < 1 || c > maxCapacity {
		return nil, invalidParam("C must be in [1,100000]")
	}
	s := &Service{
		eps:     eps,
		minPts:  minPts,
		w:       w,
		c:       c,
		points:  make(map[int64]*Point),
		adj:     make(map[int64]map[int64]bool),
		core:    make(map[int64]bool),
		label:   make(map[int64]int64),
		grid:    make(map[[2]int64]map[int64]bool),
		heapPos: make(map[int64]int),
	}
	return s, nil
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func (s *Service) cellOf(x, y int64) [2]int64 {
	return [2]int64{floorDiv(x, s.eps), floorDiv(y, s.eps)}
}

// rangeQuery 发出一次 eps 邻域查询：基于边长 eps 的网格，只需检查 9 个格子。
func (s *Service) rangeQuery(x, y int64) []int64 {
	s.rangeQueries++
	cx := floorDiv(x, s.eps)
	cy := floorDiv(y, s.eps)
	e2 := s.eps * s.eps
	var out []int64
	for dx := int64(-1); dx <= 1; dx++ {
		for dy := int64(-1); dy <= 1; dy++ {
			bucket := s.grid[[2]int64{cx + dx, cy + dy}]
			for id := range bucket {
				p := s.points[id]
				px := p.X - x
				py := p.Y - y
				if px*px+py*py <= e2 {
					out = append(out, id)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// heapLess: 出生时刻升序，相同则 id 升序（顺序确定性，与过期语义无关）。
func (s *Service) heapLess(i, j int) bool {
	pi, pj := s.points[s.heap[i]], s.points[s.heap[j]]
	if pi.Born != pj.Born {
		return pi.Born < pj.Born
	}
	return pi.ID < pj.ID
}

func (s *Service) heapSwap(i, j int) {
	s.heap[i], s.heap[j] = s.heap[j], s.heap[i]
	s.heapPos[s.heap[i]] = i
	s.heapPos[s.heap[j]] = j
}

func (s *Service) heapUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !s.heapLess(i, parent) {
			break
		}
		s.heapSwap(i, parent)
		i = parent
	}
}

func (s *Service) heapDown(i int) {
	n := len(s.heap)
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && s.heapLess(right, left) {
			smallest = right
		}
		if !s.heapLess(smallest, i) {
			return
		}
		s.heapSwap(i, smallest)
		i = smallest
	}
}

func (s *Service) heapPush(id int64) {
	s.heapPos[id] = len(s.heap)
	s.heap = append(s.heap, id)
	s.heapUp(len(s.heap) - 1)
}

func (s *Service) heapRemove(id int64) {
	pos, ok := s.heapPos[id]
	if !ok {
		return
	}
	last := len(s.heap) - 1
	if pos != last {
		s.heapSwap(pos, last)
	}
	s.heap = s.heap[:last]
	delete(s.heapPos, id)
	if pos < len(s.heap) {
		s.heapUp(pos)
		s.heapDown(pos)
	}
}

// addPointRaw 把点登记进 points/grid/heap，并发出恰好一次 rangeQuery
// 建立与现存点的双向邻接。
func (s *Service) addPointRaw(p *Point) []int64 {
	s.points[p.ID] = p
	nb := s.rangeQuery(p.X, p.Y)
	s.adj[p.ID] = make(map[int64]bool)
	for _, id := range nb {
		s.adj[p.ID][id] = true
		s.adj[id][p.ID] = true
	}
	s.adj[p.ID][p.ID] = true
	cell := s.cellOf(p.X, p.Y)
	bucket := s.grid[cell]
	if bucket == nil {
		bucket = make(map[int64]bool)
		s.grid[cell] = bucket
	}
	bucket[p.ID] = true
	s.heapPush(p.ID)
	return nb
}

// deletePointRaw 删除点的全部索引与其所有双向邻接（不再发出 rangeQuery）。
// 调用前 adj 仍完整。
func (s *Service) deletePointRaw(id int64) {
	p := s.points[id]
	for nb := range s.adj[id] {
		delete(s.adj[nb], id)
	}
	delete(s.adj, id)
	cell := s.cellOf(p.X, p.Y)
	delete(s.grid[cell], id)
	if len(s.grid[cell]) == 0 {
		delete(s.grid, cell)
	}
	s.heapRemove(id)
	delete(s.points, id)
	delete(s.core, id)
	delete(s.label, id)
}
