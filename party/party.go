// Package party 负责组队登记：名单校验、pid 唯一性与成员归属。
package party

// Party 是一支队伍，Members 保持登记时的次序（入座按此次序分位）。
type Party struct {
	ID      string
	Members []string
}

// Registry 维护全部组队及玩家到组队的唯一映射。
type Registry struct {
	byID   map[string]*Party
	byUser map[string]string
}

// NewRegistry 创建空登记表。
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]*Party), byUser: make(map[string]string)}
}

// ValidMembers 校验名单：1 到 16 个互不相同的非空玩家名。
func ValidMembers(members []string) bool {
	if len(members) < 1 || len(members) > 16 {
		return false
	}
	seen := make(map[string]bool, len(members))
	for _, name := range members {
		if name == "" || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

// Exists 报告 pid 是否已登记。
func (r *Registry) Exists(pid string) bool {
	_, ok := r.byID[pid]
	return ok
}

// Bound 报告玩家是否已属于某个组队。
func (r *Registry) Bound(player string) bool {
	_, ok := r.byUser[player]
	return ok
}

// Add 登记组队。调用方保证 pid 未存在且名单合法、成员均未归属。
func (r *Registry) Add(pid string, members []string) {
	cp := make([]string, len(members))
	copy(cp, members)
	r.byID[pid] = &Party{ID: pid, Members: cp}
	for _, name := range cp {
		r.byUser[name] = pid
	}
}

// Get 返回组队，未登记时返回 nil。
func (r *Registry) Get(pid string) *Party {
	return r.byID[pid]
}
