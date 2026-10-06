// Package registry 提供并发安全的模块登记表与基于快照的只读求解会话。
package registry

import (
	"sort"
	"sync"

	"ontology/dce/analyzer"
	"ontology/dce/dceerr"
	"ontology/dce/model"
	"ontology/dce/validate"
)

// Registry 支持多个调用方并发登记模块。
type Registry struct {
	mu      sync.Mutex
	modules map[string]*model.Module
	order   []string
}

// New 创建空登记表。
func New() *Registry {
	return &Registry{modules: map[string]*model.Module{}}
}

// Register 登记一个模块。
// 登记时执行自包含检查：类别 1（非法参数）与类别 2（重复模块），
// 命中即拒绝且不改变任何状态。类别 3/4 需要完整图，在 Solve 时统一按优先级报告。
func (r *Registry) Register(m *model.Module) error {
	if m == nil {
		return &dceerr.Error{Category: dceerr.CatInvalidArgument, Detail: "nil module"}
	}
	cp := m.Clone() // 深拷贝：拒绝或后续外部修改都不影响登记状态

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := validate.CheckModule(cp); err != nil {
		return err
	}
	if _, exists := r.modules[cp.ID]; exists {
		return &dceerr.Error{
			Category: dceerr.CatDuplicateModule, Module: cp.ID,
			Detail: "module already registered",
		}
	}
	r.modules[cp.ID] = cp
	r.order = append(r.order, cp.ID)
	return nil
}

// Session 是入口集合固定、基于创建时刻快照的只读求解会话。
type Session struct {
	snap    *model.Snapshot
	entries []string
}

// NewSession 在当前登记状态的快照上创建会话。
func (r *Registry) NewSession(entries []string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := &model.Snapshot{
		Modules: make(map[string]*model.Module, len(r.modules)),
		Order:   append([]string(nil), r.order...),
	}
	for id, m := range r.modules {
		snap.Modules[id] = m.Clone()
	}
	// 入口去重后固定；排序仅为可复现，不动点对入口集合无序。
	return &Session{snap: snap, entries: dedupSorted(entries)}
}

// SnapshotIDs 返回当前已登记模块标识的快照（主要用于测试与状态可观测）。
func (r *Registry) SnapshotIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// Solve 基于快照执行完整校验与裁剪分析。
// 构造期检查先于解析检查；入口中的未知模块在求解时按未知模块报告。
func (s *Session) Solve(opts ...analyzer.Option) (*analyzer.Result, error) {
	// 类别 3 优先报告入口中的未知模块（会话创建时入口即固定）。
	known := make(map[string]bool, len(s.snap.Modules))
	for id := range s.snap.Modules {
		known[id] = true
	}
	for _, en := range s.entries {
		if !known[en] {
			return nil, &dceerr.Error{
				Category: dceerr.CatUnknownModule, Module: en, Target: en,
				Detail: "entry module does not exist",
			}
		}
	}

	// 类别 2/3/4（类别 1 已在登记时拒绝；此处对快照再做一遍保持单一事实来源）。
	if err := validate.CheckSnapshot(s.snap, known); err != nil {
		return nil, err
	}

	return analyzer.Analyze(s.snap, s.entries, opts...)
}

func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
