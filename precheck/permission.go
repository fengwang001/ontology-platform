package precheck

// Edge 是权限继承/持有关系图中的一条有向边。
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// PermissionSnapshot 是某历史时刻的权限继承关系快照。
type PermissionSnapshot struct {
	At       Moment   `json:"at"`
	Active   []Edge   `json:"active"`
	Held     []string `json:"held"`
	Complete bool     `json:"complete"`
}

// Authorize 判断 caller 在该快照下是否持有 required 权限。
// 权限节点为不透明字符串（调用者身份与权限令牌共用一个命名空间）；
// 活跃边 "a -> p" 表示 a 直接持有 p；"a -> b" 表示 a 继承 b 的全部权限。
// 调用者持有的权限集合 = 从 caller 出发沿活跃边可达的全部节点。
// 环由 BFS 的 visited 集合自然处理。
func (snap *PermissionSnapshot) Authorize(caller, required string) bool {
	adj := make(map[string][]string, len(snap.Active))
	for _, e := range snap.Active {
		adj[e.From] = append(adj[e.From], e.To)
	}
	seen := map[string]bool{caller: true}
	queue := []string{caller}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == required {
			return true
		}
		for _, next := range adj[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}
