// Package hook 定义校验钩子及其注册表。
package hook

import "ontology/snapshot"

// Phase 钩子运行阶段。
type Phase int

const (
	// Pre 变更提交前运行，看到旧状态，失败则变更不发生。
	Pre Phase = iota
	// Post 变更应用后运行，看到新状态，失败则回滚。
	Post
)

func (p Phase) String() string {
	if p == Pre {
		return "pre"
	}
	return "post"
}

// Check 只读校验：pass 为 false 时 reason 说明原因。
type Check func(s snapshot.Snapshot) (pass bool, reason string)

// Hook 一条校验钩子。
type Hook struct {
	Name      string
	AppliesTo string // 匹配的对象类型名
	Phase     Phase
	Priority  int // 组内排序键，小者先运行
	Check     Check

	seq int // 注册序号，确定性排序的最终 tiebreak 之一
}

// Seq 返回注册序号。
func (h Hook) Seq() int { return h.seq }

// Registry 按 appliesTo 索引的钩子注册表。
type Registry struct {
	byType      map[string][]Hook
	seq         int
	matchVisits int // 非导出计数器：最近一次 Match 访问的钩子数
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{byType: make(map[string][]Hook)}
}

// Register 注册钩子，返回注册后的副本（含注册序号）。
func (r *Registry) Register(h Hook) Hook {
	h.seq = r.seq
	r.seq++
	r.byType[h.AppliesTo] = append(r.byType[h.AppliesTo], h)
	return h
}

// Match 返回适用于 objType 的全部钩子；只扫描该类型桶。
func (r *Registry) Match(objType string) []Hook {
	bucket := r.byType[objType]
	r.matchVisits = len(bucket)
	out := make([]Hook, len(bucket))
	copy(out, bucket)
	return out
}

// MatchVisits 返回最近一次 Match 访问的钩子数（匹配效率证明用）。
func (r *Registry) MatchVisits() int { return r.matchVisits }

// Len 返回已注册钩子总数。
func (r *Registry) Len() int { return r.seq }
