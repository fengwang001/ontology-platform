package ontology

// linearizeLocked 返回类型 id 的解析顺序：派生类型在前，其后为去重后的祖先。
// 解析语义：先序深度优先、有序父链、首次出现保留。
// 任何继承环都返回 false；未知祖先同样返回 false（环/悬空引用禁止进入导出）。
func (p *Platform) linearizeLocked(id string) ([]string, bool) {
	order := make([]string, 0, 4)
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var dfs func(string) bool
	dfs = func(cur string) bool {
		if visited[cur] {
			return true
		}
		if visiting[cur] {
			return false // 继承环
		}
		node, ok := p.types[cur]
		if !ok {
			return false // 悬空父引用
		}
		visiting[cur] = true
		order = append(order, cur)
		for _, parent := range node.parents {
			if !dfs(parent) {
				return false
			}
		}
		visiting[cur] = false
		visited[cur] = true
		return true
	}
	if !dfs(id) {
		return nil, false
	}
	return order, true
}
