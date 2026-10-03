// Package warming 维护集群在役/预热版本与预热期限超时结算。
package warming

import "sort"

// ClusterState 是单个集群的状态：在役版本（0 表示无）、至多一个预热版本及其 since。
type ClusterState struct {
	ServingVer   int64
	WarmingVer   int64
	WarmingSince int64
}

// Table 是全部集群的状态表。
type Table struct {
	Clusters map[string]*ClusterState
	W        int64
}

// NewTable 创建预热期限为 W 的空集群表。
func NewTable(w int64) *Table {
	return &Table{Clusters: map[string]*ClusterState{}, W: w}
}

// Clone 深拷贝整张表。
func (t *Table) Clone() *Table {
	cp := &Table{Clusters: make(map[string]*ClusterState, len(t.Clusters)), W: t.W}
	for name, s := range t.Clusters {
		state := *s
		cp.Clusters[name] = &state
	}
	return cp
}

// PutWarming 新增或更新集群：进入/取代预热版本并重置计时；在役版本不动。
func (t *Table) PutWarming(name string, ver, now int64) {
	s := t.Clusters[name]
	if s == nil {
		s = &ClusterState{}
		t.Clusters[name] = s
	}
	s.WarmingVer, s.WarmingSince = ver, now
}

// Delete 立即移除集群（连同其预热版本）。不存在为无操作。
func (t *Table) Delete(name string) {
	delete(t.Clusters, name)
}

// Get 返回集群状态拷贝；不存在时 ok=false。
func (t *Table) Get(name string) (ClusterState, bool) {
	s, ok := t.Clusters[name]
	if !ok {
		return ClusterState{}, false
	}
	return *s, true
}

// Exists 报告集群是否仍在表内。
func (t *Table) Exists(name string) bool {
	_, ok := t.Clusters[name]
	return ok
}

// Ready 报告集群预热完成：预热版本晋升为在役版本（取代旧的）。
// 无预热版本返回 false 且不改动状态。
func (t *Table) Ready(name string) bool {
	s, ok := t.Clusters[name]
	if !ok || s.WarmingVer == 0 {
		return false
	}
	s.ServingVer, s.WarmingVer, s.WarmingSince = s.WarmingVer, 0, 0
	return true
}

// ExpireDue 按 (since,name) 升序返回满足 since+W<=now 的预热集群。
func (t *Table) ExpireDue(now int64) []string {
	type key struct {
		since int64
		name  string
	}
	keys := make([]key, 0)
	for name, s := range t.Clusters {
		if s.WarmingVer != 0 && s.WarmingSince+t.W <= now {
			keys = append(keys, key{s.WarmingSince, name})
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].since != keys[j].since {
			return keys[i].since < keys[j].since
		}
		return keys[i].name < keys[j].name
	})
	due := make([]string, len(keys))
	for i, k := range keys {
		due[i] = k.name
	}
	return due
}

// DropWarming 丢弃某集群的预热版本。无在役版本则连同集群移除，返回 false；
// 有在役版本则保留（仅清预热），返回 true。无预热版本时状态不变并返回 false。
func (t *Table) DropWarming(name string) (hadServing bool) {
	s, ok := t.Clusters[name]
	if !ok || s.WarmingVer == 0 {
		return false
	}
	if s.ServingVer == 0 {
		delete(t.Clusters, name)
		return false
	}
	s.WarmingVer, s.WarmingSince = 0, 0
	return true
}

// IsReady 是“就绪”判定：有在役版本且没有预热版本。
func (t *Table) IsReady(name string) bool {
	s, ok := t.Clusters[name]
	return ok && s.ServingVer != 0 && s.WarmingVer == 0
}
