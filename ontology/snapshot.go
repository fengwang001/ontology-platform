package ontology

// Snapshot 是某序号处、订正后视图的状态快照。
//
// 快照只保存「活实例当前值」与「每实例最新有效动作序号」，
// 拷贝代价随实例数增长，与历史记录总数无关；结合最近快照可把
// 任意区间重放的扫描量限制在快照间隔（区间长度量级）以内。
type Snapshot struct {
	at     int
	state  map[string]string
	latest map[string]int
}

func snapshotFrom(at int, state map[string]string, latest map[string]int) *Snapshot {
	st := make(map[string]string, len(state))
	for k, v := range state {
		st[k] = v
	}
	lt := make(map[string]int, len(latest))
	for k, v := range latest {
		lt[k] = v
	}
	return &Snapshot{at: at, state: st, latest: lt}
}

func cloneState(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// sortedKeys 返回 map 键的确定顺序，供测试 / 打印使用。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
