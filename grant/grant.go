// Package grant 维护影视授权登记：时间窗、排除地域、独占冲突、
// 撤销截断与延期重判。
package grant

import (
	"errors"
	"sort"
	"sync"

	"ontology/territory"
)

const maxT = int64(1_000_000_000_000)

// 哨兵错误（errors.Is 可区分）。
var (
	ErrInvalidArg    = errors.New("grant: invalid argument")
	ErrClockBack     = errors.New("grant: clock moved backwards")
	ErrDuplicateID   = errors.New("grant: grant id already exists")
	ErrUnknownRegion = errors.New("grant: unknown region node")
	ErrBadExcludes   = errors.New("grant: illegal excludes")
	ErrEmptyCoverage = errors.New("grant: empty coverage")
	ErrConflict      = errors.New("grant: exclusive conflict")
	ErrNotFound      = errors.New("grant: grant not found")
	ErrExpired       = errors.New("grant: grant already expired")
	ErrNotExtended   = errors.New("grant: new end is not after current end")
)

// Grant 是一条已登记授权（当前生效状态）。
type Grant struct {
	ID        string
	Title     string
	Licensee  string
	Node      string
	Excludes  []string
	Start     int64
	End       int64
	Exclusive bool

	cover []territory.Segment
}

// ConflictError 携带字节序最小的冲突授权 id。
type ConflictError struct {
	ID string
}

func (e *ConflictError) Error() string        { return "grant: exclusive conflict with " + e.ID }
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// Registry 是授权登记簿，所有方法可并发调用。
type Registry struct {
	mu   sync.RWMutex
	tree *territory.Tree

	maxNow int64

	byID    map[string]*Grant
	byTitle map[string]map[string]*Grant

	// spans：最近一次覆盖集共叶判定触碰的段数。
	// compared：最近一次 Add/Extend 冲突扫描比较的同 title 授权数。
	spans    int
	compared int
}

// NewRegistry 基于给定不可变地域树创建登记簿。
func NewRegistry(t *territory.Tree) *Registry {
	return &Registry{
		tree:    t,
		byID:    make(map[string]*Grant),
		byTitle: make(map[string]map[string]*Grant),
	}
}

// Tree 返回登记簿绑定的地域树。
func (r *Registry) Tree() *territory.Tree { return r.tree }

// Add 登记一条新授权。
func (r *Registry) Add(now int64, id, title, licensee, node string, excludes []string, start, end int64, exclusive bool) error {
	// 1. 参数非法：字段越界 / start>=end / excludes 超限或重复 / 关键字段为空。
	if now < 0 || now > maxT || id == "" || title == "" || licensee == "" || node == "" ||
		start < 0 || start > maxT || end < 0 || end > maxT || start >= end ||
		len(excludes) > 8 || hasDup(excludes) {
		return ErrInvalidArg
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 2. 时钟回退。
	if now < r.maxNow {
		return ErrClockBack
	}
	// 3. id 已存在。
	if _, ok := r.byID[id]; ok {
		return ErrDuplicateID
	}
	// 4. 地域未知。
	if !r.tree.Has(node) {
		return ErrUnknownRegion
	}
	for _, e := range excludes {
		if !r.tree.Has(e) {
			return ErrUnknownRegion
		}
	}
	// 5. 排除非法：非真后代 / 彼此存在祖先后代关系。
	for _, e := range excludes {
		if !r.tree.IsProperDescendant(node, e) {
			return ErrBadExcludes
		}
	}
	for i := 0; i < len(excludes); i++ {
		for j := i + 1; j < len(excludes); j++ {
			if r.tree.IsProperDescendant(excludes[i], excludes[j]) ||
				r.tree.IsProperDescendant(excludes[j], excludes[i]) {
				return ErrBadExcludes
			}
		}
	}
	// 6. 覆盖为空。
	cover, err := r.tree.Cover(node, excludes)
	if err == territory.ErrEmptyCover {
		return ErrEmptyCoverage
	}
	if err != nil {
		return ErrUnknownRegion
	}

	g := &Grant{
		ID:        id,
		Title:     title,
		Licensee:  licensee,
		Node:      node,
		Excludes:  append([]string(nil), excludes...),
		Start:     start,
		End:       end,
		Exclusive: exclusive,
		cover:     cover,
	}
	// 7. 独占冲突（同 title 全量比较，取 id 字节序最小者）。
	if minID := r.findConflict(g, ""); minID != "" {
		return &ConflictError{ID: minID}
	}

	r.byID[id] = g
	bucket := r.byTitle[title]
	if bucket == nil {
		bucket = make(map[string]*Grant)
		r.byTitle[title] = bucket
	}
	bucket[id] = g
	r.maxNow = now
	return nil
}

// Revoke 令授权自 now 起失效。
func (r *Registry) Revoke(now int64, id string) error {
	if now < 0 || now > maxT || id == "" {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockBack
	}
	g, ok := r.byID[id]
	if !ok {
		return ErrNotFound
	}
	if now >= g.End {
		return ErrExpired
	}
	if now <= g.Start {
		delete(r.byID, id)
		delete(r.byTitle[g.Title], id)
	} else {
		g.End = now
	}
	r.maxNow = now
	return nil
}

// Extend 将授权延期到 newEnd，按新时间窗整条重判独占冲突。
func (r *Registry) Extend(now int64, id string, newEnd int64) error {
	if now < 0 || now > maxT || id == "" || newEnd < 0 || newEnd > maxT {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockBack
	}
	g, ok := r.byID[id]
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
	cand.End = newEnd
	if minID := r.findConflict(&cand, id); minID != "" {
		return &ConflictError{ID: minID}
	}
	g.End = newEnd
	r.maxNow = now
	return nil
}

// Active 为只读查询：返回 title 下在时刻 t 覆盖叶 leaf、且 [start,end) 包含 t
// 的全部授权，按 id 字节序排列。leaf 未知或非叶时返回错误。
func (r *Registry) Active(title, leaf string, t int64) ([]Grant, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.tree.Has(leaf) {
		return nil, ErrUnknownRegion
	}
	if !r.tree.IsLeaf(leaf) {
		return nil, ErrBadExcludes
	}
	rng, _ := r.tree.NodeRange(leaf)
	out := make([]Grant, 0)
	for _, g := range r.byTitle[title] {
		if t < g.Start || t >= g.End {
			continue
		}
		if coverContains(g.cover, rng.Lo) {
			out = append(out, *g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// findConflict 扫描同 title 全部既有授权（跳过 skipID），返回字节序最小的冲突 id。
func (r *Registry) findConflict(cand *Grant, skipID string) string {
	r.compared = 0
	minID := ""
	for _, g := range r.byTitle[cand.Title] {
		if g.ID == skipID {
			continue
		}
		r.compared++
		if g.Licensee == cand.Licensee {
			continue
		}
		if !cand.Exclusive && !g.Exclusive {
			continue
		}
		if cand.End <= g.Start || g.End <= cand.Start {
			continue
		}
		if !overlapCount(cand.cover, g.cover, r) {
			continue
		}
		if minID == "" || g.ID < minID {
			minID = g.ID
		}
	}
	return minID
}

// overlapCount 归并两段有序不相交区间集合；每次比较一对段即触碰并计入 spans，
// 触碰段数不超过 len(a)+len(b)（≤ ex1+ex2+2），与叶总数无关。
func overlapCount(a, b []territory.Segment, r *Registry) bool {
	i, j := 0, 0
	r.spans = 0
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
			return true
		}
		if a[i].Hi < b[j].Hi {
			i++
		} else if a[i].Hi > b[j].Hi {
			j++
		} else {
			i++
			j++
		}
	}
	return false
}

func coverContains(segs []territory.Segment, leafIdx int) bool {
	for _, s := range segs {
		if s.Lo <= leafIdx && leafIdx < s.Hi {
			return true
		}
	}
	return false
}

func hasDup(xs []string) bool {
	seen := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		if x == "" {
			return true
		}
		if _, ok := seen[x]; ok {
			return true
		}
		seen[x] = struct{}{}
	}
	return false
}
