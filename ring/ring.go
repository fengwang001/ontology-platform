// Package ring 实现环形多主复制的变更转发与回环抑制模拟器。
//
// N 个站点（编号 1..N）排成环，站点 i 与 i%N+1 相邻；每对相邻站点
// 之间每个方向各有一条 FIFO 链路。所有导出方法持有同一把互斥锁，
// 并发调用等价于某个串行顺序；相同操作序列重放得到相同结果。
package ring

import (
	"errors"
	"fmt"
	"sync"

	"ontology/change"
	"ontology/seen"
)

// Result 是 Write / Deliver 的仲裁结果。
type Result int

const (
	// Applied 表示变更在目标站点仲裁获胜并覆盖记录。
	Applied Result = iota
	// Lost 表示变更仲裁失败，站点记录不变（但仍被转发）。
	Lost
	// Dup 表示变更为重复（序号不超过已见下限），不改状态也不转发。
	Dup
)

func (r Result) String() string {
	switch r {
	case Applied:
		return "Applied"
	case Lost:
		return "Lost"
	case Dup:
		return "Dup"
	default:
		return fmt.Sprintf("Result(%d)", int(r))
	}
}

// 各类拒绝错误。被拒绝的调用不改变任何站点、链路、q、c 与 f。
var (
	ErrInvalidParam    = errors.New("ring: 参数非法")
	ErrNotWritable     = errors.New("ring: 站点不可写")
	ErrClockRegression = errors.New("ring: 时钟回退")
	ErrDown            = errors.New("ring: 链路断开")
	ErrEmpty           = errors.New("ring: 链路队列为空")
)

// Record 是站点键值表中一条记录的只读视图。
type Record struct {
	Op     change.Op     // Put 或 Del（Del 为墓碑）
	Val    int64         // Put 的值
	Triple change.Triple // 仲裁三元组
}

// link 是一条有向 FIFO 链路。
type link struct {
	up    bool
	queue []change.Change
}

// Sim 是环形多主复制模拟器。零值不可用，须用 New 构造。
type Sim struct {
	mu       sync.Mutex
	n        int
	writable map[int]bool
	clock    []int64 // c_i，下标 1..n
	q        []int64 // q_i，下标 1..n
	floors   []*seen.Floor
	tables   []map[string]Record
	links    map[[2]int]*link
}

// New 构造 N 个站点的环形模拟器，W 为非空可写站点集合。
func New(n int, writable []int) (*Sim, error) {
	if n < 3 || n > 8 {
		return nil, fmt.Errorf("%w: 站点数 %d 不在 [3,8]", ErrInvalidParam, n)
	}
	w := make(map[int]bool, len(writable))
	for _, s := range writable {
		if s < 1 || s > n {
			return nil, fmt.Errorf("%w: 可写站点 %d 越界", ErrInvalidParam, s)
		}
		w[s] = true
	}
	if len(w) == 0 {
		return nil, fmt.Errorf("%w: 可写站点集合为空", ErrInvalidParam)
	}
	s := &Sim{
		n:        n,
		writable: w,
		clock:    make([]int64, n+1),
		q:        make([]int64, n+1),
		floors:   make([]*seen.Floor, n+1),
		tables:   make([]map[string]Record, n+1),
		links:    make(map[[2]int]*link),
	}
	for i := 1; i <= n; i++ {
		s.floors[i] = seen.New()
		s.tables[i] = make(map[string]Record)
		s.links[[2]int{i, s.next(i)}] = &link{up: true}
		s.links[[2]int{i, s.prev(i)}] = &link{up: true}
	}
	return s, nil
}

// N 返回站点数。
func (s *Sim) N() int { return s.n }

func (s *Sim) next(i int) int { return i%s.n + 1 }

func (s *Sim) prev(i int) int { return (i-2+s.n)%s.n + 1 }

func (s *Sim) validSite(i int) bool { return i >= 1 && i <= s.n }

// adjacent 报告 from→to 是否为一条存在的有向链路（两站点相邻）。
func (s *Sim) adjacent(from, to int) bool {
	return s.validSite(from) && s.validSite(to) && (s.next(from) == to || s.prev(from) == to)
}

// arbitrate 在站点表上按三元组全序仲裁：无记录或严格更大则覆盖。
func arbitrate(table map[string]Record, c change.Change) Result {
	rec, ok := table[c.Key]
	if !ok || rec.Triple.Less(c.Triple()) {
		table[c.Key] = Record{Op: c.Op, Val: c.Val, Triple: c.Triple()}
		return Applied
	}
	return Lost
}

// Write 在站点 s 上生成变更并本地仲裁，无论胜负都向两个邻居入队。
//
// 拒绝次序：参数非法 → 权限不足 → 时钟回退。
func (s *Sim) Write(site int, key string, op change.Op, val int64, now int64) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validSite(site) || !change.ValidKey(key) || !op.Valid() || !change.ValidNow(now) {
		return Lost, ErrInvalidParam
	}
	if !s.writable[site] {
		return Lost, ErrNotWritable
	}
	if now < s.clock[site] {
		return Lost, ErrClockRegression
	}
	s.q[site]++
	c := change.Change{Origin: site, Seq: s.q[site], Ts: now, Key: key, Op: op, Val: val}
	s.clock[site] = now
	s.floors[site].SetOwn(site, c.Seq)
	res := arbitrate(s.tables[site], c)
	s.links[[2]int{site, s.next(site)}].queue = append(s.links[[2]int{site, s.next(site)}].queue, c)
	s.links[[2]int{site, s.prev(site)}].queue = append(s.links[[2]int{site, s.prev(site)}].queue, c)
	return res, nil
}

// Deliver 弹出链路 from→to 的队头变更并在 to 上处理。
//
// 拒绝次序：参数非法 → ErrDown → ErrEmpty。
// 非重复时无论仲裁胜负都向 to 的另一个邻居（不是 from）入队一份；
// 变更绕环回到来源站点同样走 f 比较得 Dup，不做特判。
func (s *Sim) Deliver(from, to int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.adjacent(from, to) {
		return Dup, ErrInvalidParam
	}
	l := s.links[[2]int{from, to}]
	if !l.up {
		return Dup, ErrDown
	}
	if len(l.queue) == 0 {
		return Dup, ErrEmpty
	}
	c := l.queue[0]
	l.queue = l.queue[1:]
	f := s.floors[to]
	if f.IsDup(c.Origin, c.Seq) {
		return Dup, nil
	}
	f.Advance(c.Origin, c.Seq) // 序号不连续属内部错误，panic 暴露
	res := arbitrate(s.tables[to], c)
	other := s.next(to)
	if other == from {
		other = s.prev(to)
	}
	s.links[[2]int{to, other}].queue = append(s.links[[2]int{to, other}].queue, c)
	return res, nil
}

// SetLink 断开或恢复链路 from→to；断开期间队列内容保留。
func (s *Sim) SetLink(from, to int, up bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.adjacent(from, to) {
		return ErrInvalidParam
	}
	s.links[[2]int{from, to}].up = up
	return nil
}

// Floor 返回 f_s[o]（只读）。站点越界时返回 0。
func (s *Sim) Floor(site, origin int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validSite(site) {
		return 0
	}
	return s.floors[site].Get(origin)
}

// Get 返回站点 s 上键 key 的记录与是否存在（只读）。
func (s *Sim) Get(site int, key string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validSite(site) {
		return Record{}, false
	}
	rec, ok := s.tables[site][key]
	return rec, ok
}

// Pending 返回链路 from→to 队列的拷贝（只读）；链路不存在时返回 nil。
func (s *Sim) Pending(from, to int) []change.Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.links[[2]int{from, to}]
	if !ok {
		return nil
	}
	out := make([]change.Change, len(l.queue))
	copy(out, l.queue)
	return out
}

// Writable 报告站点是否在可写集合 W 中（只读）。
func (s *Sim) Writable(site int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writable[site]
}

// WriteCount 返回站点 s 的写计数 q_s（只读）。
func (s *Sim) WriteCount(site int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validSite(site) {
		return 0
	}
	return s.q[site]
}

// LinkUp 报告链路 from→to 是否连通（只读）。
func (s *Sim) LinkUp(from, to int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.links[[2]int{from, to}]
	return ok && l.up
}
