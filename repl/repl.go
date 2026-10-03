// Package repl 在 region 与 link 之上编排全互联双向跨区域复制：
// 创建版本、删除标记、入队、投递 Apply、失败重试与收敛查询。
package repl

import (
	"errors"
	"sync"

	"ontology/link"
	"ontology/region"
)

// Mode 是创建时的积压取舍模式。
type Mode int

const (
	// Strict：任一应入队链路超预算则整次创建拒绝。
	Strict Mode = iota
	// Relaxed：本地照常创建，超限链路丢项并计 lost。
	Relaxed
)

// Params 为系统构造参数。
type Params struct {
	Regions    []string
	MarkerRepl bool
	Mode       Mode
	C          int64
	R          int
}

var (
	// ErrInvalidArg 参数非法（校验次序最先）。
	ErrInvalidArg = errors.New("repl: invalid argument")
	// ErrNoRegion 区域不存在。
	ErrNoRegion = errors.New("repl: unknown region")
	// ErrBacklog 积压超限。
	ErrBacklog = errors.New("repl: backlog limit exceeded")
	// ErrNotFound 失败列表中无该标识。
	ErrNotFound = errors.New("repl: failed item not found")
)

// GetResult 区分当前版本为数据、删除标记与键不存在。
type GetResult struct {
	Exists  bool
	Deleted bool
	Version region.Version
}

// System 是全互联复制系统。
type System struct {
	mu           sync.RWMutex
	params       Params
	regions      map[string]*region.Region
	links        map[[2]string]*link.Link
	deliverLocks map[[2]string]*sync.Mutex
	order        []string
}

// New 按参数构造系统；非法参数返回包装 ErrInvalidArg 的错误。
func New(p Params) (*System, error) {
	if len(p.Regions) < 2 || len(p.Regions) > 4 ||
		p.C < 1 || p.C > 1e12 || p.R < 1 || p.R > 100 {
		return nil, ErrInvalidArg
	}
	seen := make(map[string]struct{}, len(p.Regions))
	for _, r := range p.Regions {
		if r == "" {
			return nil, ErrInvalidArg
		}
		if _, dup := seen[r]; dup {
			return nil, ErrInvalidArg
		}
		seen[r] = struct{}{}
	}
	sys := &System{
		params:       p,
		regions:      make(map[string]*region.Region, len(p.Regions)),
		links:        make(map[[2]string]*link.Link),
		deliverLocks: make(map[[2]string]*sync.Mutex),
		order:        append([]string(nil), p.Regions...),
	}
	for _, r := range p.Regions {
		sys.regions[r] = region.New(r)
	}
	for _, src := range p.Regions {
		for _, dst := range p.Regions {
			if src != dst {
				key := [2]string{src, dst}
				sys.links[key] = link.New(src, dst, p.C, p.R)
				sys.deliverLocks[key] = &sync.Mutex{}
			}
		}
	}
	return sys, nil
}

// Put 在区域 r 创建大小 size、时间戳 ts 的数据版本。
func (s *System) Put(r, key string, size, ts int64) (region.VersionID, error) {
	if err := validateCreate(key, size, ts); err != nil {
		return region.VersionID{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.regions[r]
	if !ok {
		return region.VersionID{}, ErrNoRegion
	}
	return s.createLocked(g, key, size, ts, false)
}

// Delete 在区域 r 为 key 创建删除标记（size 0）；键无任何版本也照建。
func (s *System) Delete(r, key string, ts int64) (region.VersionID, error) {
	if err := validateCreate(key, 0, ts); err != nil {
		return region.VersionID{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.regions[r]
	if !ok {
		return region.VersionID{}, ErrNoRegion
	}
	return s.createLocked(g, key, 0, ts, true)
}

// createLocked 调用方已校验参数且持有 s.mu。
func (s *System) createLocked(g *region.Region, key string, size, ts int64, del bool) (region.VersionID, error) {
	origin := g.Name()
	targets := s.destLinksLocked(origin, del)
	if s.params.Mode == Strict {
		for _, lk := range targets {
			if !lk.CanEnqueue(size) {
				return region.VersionID{}, ErrBacklog
			}
		}
	}
	v := g.Create(key, size, ts, del)
	for _, lk := range targets {
		if lk.CanEnqueue(size) {
			lk.Enqueue(v)
		} else {
			lk.MarkLost()
		}
	}
	return v.ID, nil
}

// destLinksLocked 返回 origin 之外各目的区域链路（MarkerRepl 关闭时标记无链路），
// 按区域构造顺序返回，保证行为可重放。
func (s *System) destLinksLocked(origin string, del bool) []*link.Link {
	if del && !s.params.MarkerRepl {
		return nil
	}
	out := make([]*link.Link, 0, len(s.order)-1)
	for _, dst := range s.order {
		if dst == origin {
			continue
		}
		out = append(out, s.links[[2]string{origin, dst}])
	}
	return out
}

// Deliver 沿 src->dst 链路至多调用 n 次 send：
// 每次作用于当前队首；applied 或 err==nil 即 Apply；err==nil 出队，
// err!=nil 计数，<R 阻塞结束，==R 失败转移后继续后续项。
func (s *System) Deliver(src, dst string, n int, send link.SendFunc) (sends int, err error) {
	if n < 1 || n > 1000 {
		return 0, ErrInvalidArg
	}
	s.mu.RLock()
	_, ok1 := s.regions[src]
	gDst, ok2 := s.regions[dst]
	lk := s.links[[2]string{src, dst}]
	dl := s.deliverLocks[[2]string{src, dst}]
	s.mu.RUnlock()
	if src == dst || !ok1 || !ok2 || lk == nil {
		return 0, ErrInvalidArg
	}
	dl.Lock()
	defer dl.Unlock()
	for sends < n {
		item, ok := lk.Peek()
		if !ok {
			return sends, nil
		}
		applied, sendErr := send(item)
		sends++
		if applied || sendErr == nil {
			if dup := gDst.Apply(item.Ver); dup {
				lk.AddDup()
			}
		}
		if sendErr == nil {
			lk.PopFront()
			continue
		}
		tries := lk.RecordTrial()
		if tries < s.params.R {
			return sends, nil
		}
		lk.TransferHeadToFailed()
	}
	return sends, nil
}

// Retry 次序：参数非法 > 不存在 > 积压超限。
// 把失败项清零重试计数重新入队尾。
func (s *System) Retry(src, dst string, id region.VersionID) error {
	if id.Origin == "" || id.Seq < 1 {
		return ErrInvalidArg
	}
	s.mu.RLock()
	_, ok1 := s.regions[src]
	_, ok2 := s.regions[dst]
	lk := s.links[[2]string{src, dst}]
	s.mu.RUnlock()
	if src == dst || !ok1 || !ok2 || lk == nil {
		return ErrInvalidArg
	}
	v, ok := lk.PeekFailed(id)
	if !ok {
		return ErrNotFound
	}
	if !lk.CanEnqueue(v.Size) {
		return ErrBacklog
	}
	taken, ok := lk.TakeFailed(id)
	if !ok {
		return ErrNotFound
	}
	lk.Requeue(taken)
	return nil
}

// Get 返回区域 r 内 key 的当前版本状态：
// 无任何版本 Exists=false；当前为删除标记 Deleted=true；否则为数据版本。
func (s *System) Get(r, key string) (GetResult, error) {
	if key == "" {
		return GetResult{}, ErrInvalidArg
	}
	s.mu.RLock()
	g, ok := s.regions[r]
	s.mu.RUnlock()
	if !ok {
		return GetResult{}, ErrNoRegion
	}
	v, exists := g.Current(key)
	if !exists {
		return GetResult{Exists: false}, nil
	}
	return GetResult{Exists: true, Deleted: v.Delete, Version: v}, nil
}

// Diverged 为真当且仅当各区域对 key 的当前版本标识不全相同；
// 所有区域都无该键为假。
func (s *System) Diverged(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var first region.VersionID
	have := false
	for _, name := range s.order {
		v, ok := s.regions[name].Current(key)
		if !ok {
			if have {
				return true
			}
			continue
		}
		if !have {
			first, have = v.ID, true
			continue
		}
		if v.ID != first {
			return true
		}
	}
	return false
}

// Regions 返回区域名列表（构造顺序）。
func (s *System) Regions() []string {
	return append([]string(nil), s.order...)
}

func validateCreate(key string, size, ts int64) error {
	if key == "" || size < 0 || size > 1e9 || ts < 0 || ts > 1e12 {
		return ErrInvalidArg
	}
	return nil
}

// LinkStats 是一条链路的计数快照。
type LinkStats struct {
	Enqueued  int64
	Delivered int64
	Transfers int64
	Queued    int64
	Backlog   int64
	Lost      int64
	Dup       int64
}

// QueueItem 是在队项的快照（含重试计数）。
type QueueItem struct {
	Ver   region.Version
	Tries int
}

// LinkSnapshot 是一条链路的完整快照。
type LinkSnapshot struct {
	Stats  LinkStats
	Queue  []QueueItem
	Failed []region.Version
}

// Snapshot 是整个系统的可比较状态快照，供测试与朴素模型对照。
type Snapshot struct {
	Regions  []string
	Versions map[string][]region.Version
	NextSeq  map[string]int64
	Links    map[[2]string]LinkSnapshot
}

// Snapshot 取全系统深拷贝快照（在同一把读锁内，保证一致性）。
func (s *System) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{
		Regions:  append([]string(nil), s.order...),
		Versions: make(map[string][]region.Version, len(s.order)),
		NextSeq:  make(map[string]int64, len(s.order)),
		Links:    make(map[[2]string]LinkSnapshot, len(s.links)),
	}
	for _, name := range s.order {
		vs := s.regions[name].Versions()
		sortSnapVersions(vs)
		snap.Versions[name] = vs
		snap.NextSeq[name] = s.regions[name].NextSeq()
	}
	for key, lk := range s.links {
		enq, del, tr, q, bl := lk.Counts()
		queue := lk.Queue()
		qi := make([]QueueItem, len(queue))
		for i, it := range queue {
			qi[i] = QueueItem{Ver: it.Ver, Tries: it.Tries}
		}
		snap.Links[key] = LinkSnapshot{
			Stats: LinkStats{
				Enqueued:  enq,
				Delivered: del,
				Transfers: tr,
				Queued:    q,
				Backlog:   bl,
				Lost:      lk.Lost(),
				Dup:       lk.Dup(),
			},
			Queue:  qi,
			Failed: lk.Failed(),
		}
	}
	return snap
}

func sortSnapVersions(vs []region.Version) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && snapLess(vs[j], vs[j-1]); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

func snapLess(a, b region.Version) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.ID.Origin != b.ID.Origin {
		return a.ID.Origin < b.ID.Origin
	}
	return a.ID.Seq < b.ID.Seq
}
