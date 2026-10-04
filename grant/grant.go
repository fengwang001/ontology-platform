package grant

import (
	"bytes"
	"errors"
	"sort"
	"sync"

	"ontology/territory"
)

// 时间与排除数量边界（见题设）。
const (
	MinTime = 0
	MaxTime = 1_000_000_000_000
)

var (
	// ErrInvalidArgument 表示字段越界、start>=end、excludes 超量或重复。
	ErrInvalidArgument = errors.New("grant: invalid argument")
	// ErrClockMovedBack 表示 now 小于已接受操作的最大 now。
	ErrClockMovedBack = errors.New("grant: clock moved back")
	// ErrDuplicateID 表示 Add 时授权 id 已存在。
	ErrDuplicateID = errors.New("grant: grant id already exists")
	// ErrUnknownNode 表示 node 或排除节点不在树中。
	ErrUnknownNode = errors.New("grant: unknown territory node")
	// ErrInvalidExclude 表示排除节点不是真后代或彼此有祖先后代关系。
	ErrInvalidExclude = errors.New("grant: invalid exclude node")
	// ErrEmptyCoverage 表示排除后覆盖集为空。
	ErrEmptyCoverage = errors.New("grant: coverage is empty")
	// ErrNotFound 表示 Revoke/Extend 的授权不存在。
	ErrNotFound = errors.New("grant: grant not found")
	// ErrExpired 表示授权已到期（end<=now）或 now>=end。
	ErrExpired = errors.New("grant: grant already expired")
	// ErrNotExtended 表示 newEnd 未严格大于原 end。
	ErrNotExtended = errors.New("grant: new end does not extend grant")
	// ErrNotALeaf 表示查询/覆盖判定使用了非叶节点。
	ErrNotALeaf = errors.New("grant: node is not a leaf")
)

// Grant 是一条已登记的授权。
type Grant struct {
	ID        []byte
	Title     []byte
	Licensee  []byte
	Node      string
	Excludes  []string
	Start     int64
	End       int64
	Exclusive bool

	cover []territory.Span
	holes []territory.Span
}

// Conflict 描述一次独占冲突；Grant 为冲突的既有授权（字节序最小者）。
type Conflict struct {
	Grant *Grant
}

// ConflictError 可由 errors.As 取出冲突的最小 id 授权。
type ConflictError interface {
	error
	Conflict() *Grant
}

type conflictError struct{ g *Grant }

func (e *conflictError) Error() string    { return "grant: exclusive conflict" }
func (e *conflictError) Conflict() *Grant { return e.g }

// Registry 是授权登记处，并发安全。
type Registry struct {
	mu       sync.RWMutex
	tree     *territory.Tree
	maxNow   int64
	byID     map[string]*Grant
	titles   map[string][]*Grant
	spans    int
	compared int
}

// NewRegistry 创建基于给定不可变地域树的登记处。
func NewRegistry(tree *territory.Tree) *Registry {
	return &Registry{
		tree:   tree,
		byID:   make(map[string]*Grant),
		titles: make(map[string][]*Grant),
	}
}

// Add 登记一条授权；失败返回的错误可用 errors.Is 区分。
func (r *Registry) Add(now int64, id, title, licensee []byte, node string, excludes []string, start, end int64, exclusive bool) (*Grant, error) {
	if !validTime(now) || !validTime(start) || !validTime(end) || start >= end ||
		len(id) == 0 || len(title) == 0 || len(licensee) == 0 || node == "" ||
		len(excludes) > territory.MaxExclude || hasDupString(excludes) {
		return nil, ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return nil, ErrClockMovedBack
	}
	key := string(id)
	if _, ok := r.byID[key]; ok {
		return nil, ErrDuplicateID
	}
	if !r.tree.Has(node) {
		return nil, ErrUnknownNode
	}
	for _, ex := range excludes {
		if !r.tree.Has(ex) {
			return nil, ErrUnknownNode
		}
	}
	for _, ex := range excludes {
		if !r.tree.IsProperDescendant(ex, node) {
			return nil, ErrInvalidExclude
		}
	}
	for i := 0; i < len(excludes); i++ {
		for j := i + 1; j < len(excludes); j++ {
			if r.tree.IsProperDescendant(excludes[i], excludes[j]) ||
				r.tree.IsProperDescendant(excludes[j], excludes[i]) {
				return nil, ErrInvalidExclude
			}
		}
	}

	keep, holes := r.tree.Cover(node, excludes)
	if len(keep) == 0 {
		return nil, ErrEmptyCoverage
	}

	g := &Grant{
		ID:        append([]byte(nil), id...),
		Title:     append([]byte(nil), title...),
		Licensee:  append([]byte(nil), licensee...),
		Node:      node,
		Excludes:  append([]string(nil), excludes...),
		Start:     start,
		End:       end,
		Exclusive: exclusive,
		cover:     keep,
		holes:     holes,
	}
	if c := r.firstConflict(g); c != nil {
		return nil, &conflictError{g: c}
	}

	r.byID[key] = g
	tk := string(title)
	list := append(r.titles[tk], g)
	sort.Slice(list, func(i, j int) bool { return bytes.Compare(list[i].ID, list[j].ID) < 0 })
	r.titles[tk] = list
	r.maxNow = now
	return cloneGrant(g), nil
}

// Revoke 令授权自 now 起失效（截断 end），now<=start 时整条删除。
func (r *Registry) Revoke(now int64, id []byte) error {
	if !validTime(now) || len(id) == 0 {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return ErrClockMovedBack
	}
	key := string(id)
	g, ok := r.byID[key]
	if !ok {
		return ErrNotFound
	}
	if now >= g.End {
		return ErrExpired
	}
	if now <= g.Start {
		delete(r.byID, key)
		tk := string(g.Title)
		list := r.titles[tk]
		for i, cand := range list {
			if cand == g {
				r.titles[tk] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(r.titles[tk]) == 0 {
			delete(r.titles, tk)
		}
	} else {
		g.End = now
	}
	r.maxNow = now
	return nil
}

// Extend 将授权 end 延长至 newEnd，并按新窗整条重新判定独占冲突。
func (r *Registry) Extend(now int64, id []byte, newEnd int64) error {
	if !validTime(now) || !validTime(newEnd) || len(id) == 0 {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return ErrClockMovedBack
	}
	g, ok := r.byID[string(id)]
	if !ok {
		return ErrNotFound
	}
	if g.End <= now {
		return ErrExpired
	}
	if !(newEnd > g.End) {
		return ErrNotExtended
	}

	cand := *g
	cand.ID = append([]byte(nil), g.ID...)
	cand.Title = append([]byte(nil), g.Title...)
	cand.Licensee = append([]byte(nil), g.Licensee...)
	cand.cover = g.cover
	cand.holes = g.holes
	cand.End = newEnd
	if c := r.firstConflict(&cand); c != nil {
		return &conflictError{g: c}
	}
	g.End = newEnd
	r.maxNow = now
	return nil
}

// Get 返回 id 对应授权的快照副本；不存在返回 nil。
func (r *Registry) Get(id []byte) *Grant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.byID[string(id)]
	if !ok {
		return nil
	}
	return cloneGrant(g)
}

// ActiveAt 返回 title 下覆盖叶 leaf 且在时刻 t 生效的授权快照，按 id 字节序。
func (r *Registry) ActiveAt(title, leaf []byte, t int64) []*Grant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx, ok := r.leafIndex(string(leaf))
	if !ok {
		return nil
	}
	var out []*Grant
	for _, g := range r.titles[string(title)] {
		if g.Start <= t && t < g.End && spansContain(g.cover, idx) {
			out = append(out, cloneGrant(g))
		}
	}
	return out
}

// CheckLeaf 校验 leaf 是树中存在的叶：未知报 ErrUnknownNode，存在但非叶报 ErrNotALeaf。
func (r *Registry) CheckLeaf(leaf []byte) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	code := string(leaf)
	if !r.tree.Has(code) {
		return ErrUnknownNode
	}
	if !r.tree.IsLeaf(code) {
		return ErrNotALeaf
	}
	return nil
}

// Stats 是非导出计数器的只读快照，供复杂度证明测试使用。
type Stats struct {
	Spans    int
	Compared int
}

func (r *Registry) stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Stats{Spans: r.spans, Compared: r.compared}
}

func (r *Registry) resetCounters() {
	r.mu.Lock()
	r.spans, r.compared = 0, 0
	r.mu.Unlock()
}

func validTime(t int64) bool { return t >= MinTime && t <= MaxTime }

func hasDupString(xs []string) bool {
	seen := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			return true
		}
		seen[x] = struct{}{}
	}
	return false
}

func (r *Registry) leafIndex(leaf string) (int, bool) {
	sp, ok := r.tree.LeafRange(leaf)
	if !ok || sp.Hi != sp.Lo+1 || !r.tree.IsLeaf(leaf) {
		return 0, false
	}
	return sp.Lo, true
}

// spansOverlap 以双指针归并判定两组有序不相交区间是否有公共叶，
// 每轮至少推进一个区间头，触碰区间数 ≤ len(a)+len(b)。
func (r *Registry) spansOverlap(a, b []territory.Span) bool {
	i, j := 0, 0
	found := false
	for i < len(a) && j < len(b) {
		r.spans++
		lo := a[i].Lo
		if b[j].Lo > lo {
			lo = b[j].Lo
		}
		hi := a[i].Hi
		if b[j].Hi < hi {
			hi = b[j].Hi
		}
		if lo < hi {
			found = true
		}
		switch {
		case a[i].Hi <= b[j].Hi:
			i++
		default:
			j++
		}
	}
	return found
}

func spansContain(spans []territory.Span, idx int) bool {
	i := sort.Search(len(spans), func(k int) bool { return spans[k].Hi > idx })
	return i < len(spans) && spans[i].Lo <= idx
}

// firstConflict 在同一 title 的既有授权中找字节序最小的冲突者。
// self 非空时跳过同一指针（Extend 排除自身）。
func (r *Registry) firstConflict(self *Grant) *Grant {
	var winner *Grant
	for _, g := range r.titles[string(self.Title)] {
		if g == self {
			continue
		}
		r.compared++
		if conflicts(g, self) && r.spansOverlap(g.cover, self.cover) {
			if winner == nil || bytes.Compare(g.ID, winner.ID) < 0 {
				winner = g
			}
		}
	}
	return winner
}

func conflicts(a, b *Grant) bool {
	if bytes.Equal(a.Licensee, b.Licensee) {
		return false
	}
	if !a.Exclusive && !b.Exclusive {
		return false
	}
	return a.Start < b.End && b.Start < a.End
}

func cloneGrant(g *Grant) *Grant {
	cp := *g
	cp.ID = append([]byte(nil), g.ID...)
	cp.Title = append([]byte(nil), g.Title...)
	cp.Licensee = append([]byte(nil), g.Licensee...)
	cp.Node = g.Node
	cp.Excludes = append([]string(nil), g.Excludes...)
	cp.cover = append([]territory.Span(nil), g.cover...)
	cp.holes = append([]territory.Span(nil), g.holes...)
	return &cp
}
