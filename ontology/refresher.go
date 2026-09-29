// Package ontology 提供视图依赖有向无环图的按批拓扑增量刷新器。
package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	ErrEmptyName         = errors.New("ontology: 名称为空")
	ErrDuplicateName     = errors.New("ontology: 名称重复")
	ErrUnknownName       = errors.New("ontology: 未知名称")
	ErrUnknownBase       = errors.New("ontology: 未知基底名")
	ErrUnknownDependency = errors.New("ontology: 未知依赖名")
	ErrDependencyCycle   = errors.New("ontology: 依赖成环")
	ErrTooManyViews      = errors.New("ontology: 视图数超限")
)

// ChangeKind 区分变更日志中的撤回与建立。
type ChangeKind int

const (
	// Retract 撤回旧值。
	Retract ChangeKind = iota
	// Assert 建立新值。
	Assert
)

func (k ChangeKind) String() string {
	if k == Retract {
		return "RETRACT"
	}
	return "ASSERT"
}

// Change 是一条变更日志：重算某视图时先撤回旧值再建立新值。
type Change struct {
	View  string
	Kind  ChangeKind
	Value int64
}

// view 是一个派生视图：值等于其全部直接依赖当前值之和。
type view struct {
	deps  []string
	value int64
}

// Refresher 是并发安全的按批拓扑增量刷新器。
type Refresher struct {
	mu sync.RWMutex

	bases     map[string]int64
	baseOrder []string
	views     map[string]*view
	viewOrder []string
	// dependents[x] 为直接依赖 x 的视图列表（反向边）。
	dependents map[string][]string

	maxViews int

	dirty        map[string]bool
	pending      map[string]int64
	pendingOrder []string

	log []Change
}

// NewRefresher 创建刷新器，maxViews 为视图数上限（<=0 表示不限）。
func NewRefresher(maxViews int) *Refresher {
	return &Refresher{
		bases:      make(map[string]int64),
		views:      make(map[string]*view),
		dependents: make(map[string][]string),
		maxViews:   maxViews,
		dirty:      make(map[string]bool),
		pending:    make(map[string]int64),
	}
}

// AddBase 登记基底及其初始值。
func (r *Refresher) AddBase(name string, initial int64) error {
	if name == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bases[name]; ok {
		return fmt.Errorf("%w: %q 已是基底", ErrDuplicateName, name)
	}
	if _, ok := r.views[name]; ok {
		return fmt.Errorf("%w: %q 已是视图", ErrDuplicateName, name)
	}
	r.bases[name] = initial
	r.baseOrder = append(r.baseOrder, name)
	return nil
}

// AddView 登记视图；对已存在的视图名视为重定义其依赖。
// 校验全部通过后才修改状态，失败不改变脏集、日志与已登记变更。
func (r *Refresher) AddView(name string, deps ...string) error {
	if name == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bases[name]; ok {
		return fmt.Errorf("%w: %q 已是基底", ErrDuplicateName, name)
	}
	_, redefine := r.views[name]
	if !redefine && r.maxViews > 0 && len(r.views) >= r.maxViews {
		return fmt.Errorf("%w: 上限 %d", ErrTooManyViews, r.maxViews)
	}
	seen := make(map[string]bool, len(deps))
	for _, d := range deps {
		if d == "" {
			return ErrEmptyName
		}
		if d == name {
			return fmt.Errorf("%w: %q 依赖自身", ErrDependencyCycle, name)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		if _, ok := r.bases[d]; ok {
			continue
		}
		if _, ok := r.views[d]; ok {
			continue
		}
		return fmt.Errorf("%w: %q", ErrUnknownDependency, d)
	}
	// 环检测：从每个依赖出发沿视图边可达 name 则成环。
	for _, d := range deps {
		if r.reaches(d, name, make(map[string]bool)) {
			return fmt.Errorf("%w: %q 经 %q 回到自身", ErrDependencyCycle, name, d)
		}
	}
	if redefine {
		for _, d := range r.views[name].deps {
			r.dependents[d] = removeString(r.dependents[d], name)
		}
	} else {
		r.viewOrder = append(r.viewOrder, name)
	}
	cp := append([]string(nil), deps...)
	if old, ok := r.views[name]; ok {
		old.deps = cp
	} else {
		r.views[name] = &view{deps: cp}
	}
	for _, d := range deps {
		if !containsString(r.dependents[d], name) {
			r.dependents[d] = append(r.dependents[d], name)
		}
	}
	// 依赖变化后该视图及其下游不再可信，标脏待下批重算。
	r.markDirty(name)
	return nil
}

// reaches 沿视图依赖边判断 from 是否可到达 target（调用方须持锁）。
func (r *Refresher) reaches(from, target string, visited map[string]bool) bool {
	if from == target {
		return true
	}
	if visited[from] {
		return false
	}
	visited[from] = true
	v, ok := r.views[from]
	if !ok {
		return false
	}
	for _, d := range v.deps {
		if r.reaches(d, target, visited) {
			return true
		}
	}
	return false
}

// markDirty 把 name（视图）及其全部下游视图标脏（调用方须持锁）。
func (r *Refresher) markDirty(name string) {
	stack := []string{name}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, isView := r.views[n]; isView {
			if r.dirty[n] {
				continue
			}
			r.dirty[n] = true
		}
		stack = append(stack, r.dependents[n]...)
	}
}

// SetBase 登记一次基底变更：只记录并标脏，不立刻刷新；
// 同一批内对同一基底多次设值只最后一次生效。
func (r *Refresher) SetBase(name string, value int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bases[name]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownBase, name)
	}
	if _, ok := r.pending[name]; !ok {
		r.pendingOrder = append(r.pendingOrder, name)
	}
	r.pending[name] = value
	r.markDirty(name)
	return nil
}

// Refresh 把本批累计的变更一次处理完：每个脏视图按拓扑序恰好重算一次，
// 重算前先输出撤回旧值再输出建立新值，返回本批变更日志。
func (r *Refresher) Refresh() []Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 && len(r.dirty) == 0 {
		return nil
	}
	for _, b := range r.pendingOrder {
		r.bases[b] = r.pending[b]
	}
	order := r.topoDirty()
	batch := make([]Change, 0, 2*len(order))
	for _, name := range order {
		v := r.views[name]
		var sum int64
		for _, d := range v.deps {
			sum += r.valueOf(d)
		}
		batch = append(batch,
			Change{View: name, Kind: Retract, Value: v.value},
			Change{View: name, Kind: Assert, Value: sum},
		)
		v.value = sum
	}
	r.log = append(r.log, batch...)
	r.dirty = make(map[string]bool)
	r.pending = make(map[string]int64)
	r.pendingOrder = nil
	return batch
}

// topoDirty 对脏视图做 Kahn 拓扑排序（调用方须持锁）。
// 只统计脏视图之间的边；并列时按登记顺序，保证结果可复现。
func (r *Refresher) topoDirty() []string {
	indegree := make(map[string]int, len(r.dirty))
	for name := range r.dirty {
		indegree[name] = 0
	}
	for name := range r.dirty {
		for _, d := range r.views[name].deps {
			if _, ok := r.views[d]; ok && r.dirty[d] {
				indegree[name]++
			}
		}
	}
	var ready []string
	for _, name := range r.viewOrder {
		if r.dirty[name] && indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	order := make([]string, 0, len(r.dirty))
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		for _, dep := range r.dependents[n] {
			if !r.dirty[dep] {
				continue
			}
			indegree[dep]--
			if indegree[dep] == 0 {
				ready = append(ready, dep)
			}
		}
	}
	return order
}

// valueOf 返回基底或视图的当前值（调用方须持锁）。
func (r *Refresher) valueOf(name string) int64 {
	if b, ok := r.bases[name]; ok {
		return b
	}
	return r.views[name].value
}

func removeString(s []string, x string) []string {
	out := s[:0]
	for _, v := range s {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func containsString(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// Get 查询基底或视图的当前值。
func (r *Refresher) Get(name string) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if b, ok := r.bases[name]; ok {
		return b, nil
	}
	if v, ok := r.views[name]; ok {
		return v.value, nil
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownName, name)
}

// Snapshot 返回全部基底与视图当前值的副本。
func (r *Refresher) Snapshot() map[string]int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int64, len(r.bases)+len(r.views))
	for name, b := range r.bases {
		out[name] = b
	}
	for name, v := range r.views {
		out[name] = v.value
	}
	return out
}

// DirtySet 返回当前脏视图名（按字典序）。
func (r *Refresher) DirtySet() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.dirty))
	for name := range r.dirty {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Changes 返回累计变更日志的副本。
func (r *Refresher) Changes() []Change {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Change(nil), r.log...)
}

// SelfCheck 自底向上全量重算所有视图并与当前值逐一核对。
// 只读操作，可与查询并发调用。
func (r *Refresher) SelfCheck() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	recomputed := make(map[string]int64, len(r.views))
	indegree := make(map[string]int, len(r.views))
	for name, v := range r.views {
		for _, d := range v.deps {
			if _, ok := r.views[d]; ok {
				indegree[name]++
			}
		}
	}
	var ready []string
	for _, name := range r.viewOrder {
		if indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		var sum int64
		for _, d := range r.views[n].deps {
			if b, ok := r.bases[d]; ok {
				sum += b
			} else {
				sum += recomputed[d]
			}
		}
		recomputed[n] = sum
		for _, dep := range r.dependents[n] {
			if _, ok := r.views[dep]; !ok {
				continue
			}
			indegree[dep]--
			if indegree[dep] == 0 {
				ready = append(ready, dep)
			}
		}
	}
	if len(recomputed) != len(r.views) {
		return fmt.Errorf("%w: 自检拓扑排序仅覆盖 %d/%d 个视图",
			ErrDependencyCycle, len(recomputed), len(r.views))
	}
	for _, name := range r.viewOrder {
		if got := r.views[name].value; got != recomputed[name] {
			return fmt.Errorf("ontology: 自检失败: 视图 %q 当前值 %d != 全量重算值 %d",
				name, got, recomputed[name])
		}
	}
	return nil
}
