package exam

// MutexIndex 维护互斥组成员关系与题目邻接表。
// 互斥关系按组传递闭包取并集：两题互斥当且仅当它们在
// “同组即连边”的图上处于同一连通分量。
//
// 复杂度保证：候选题与试卷题目集合的互斥判定只在候选题所在
// 连通分量内做 BFS，访问的顶点/边数与题库题目总数、互斥组总数无关。
type MutexIndex struct {
	groups     map[string]map[string]bool // 组 -> 成员
	membership map[string]map[string]bool // 题 -> 所属组
	adj        map[string]map[string]bool // 题 -> 直接互斥邻居
}

func newMutexIndex() *MutexIndex {
	return &MutexIndex{
		groups:     map[string]map[string]bool{},
		membership: map[string]map[string]bool{},
		adj:        map[string]map[string]bool{},
	}
}

// setGroups 全量重置一道题的互斥组归属，增量维护邻接表。
func (m *MutexIndex) setGroups(qid string, gs []string) {
	for g := range m.membership[qid] {
		m.leaveGroup(qid, g)
	}
	seen := map[string]bool{}
	for _, g := range gs {
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		m.joinGroup(qid, g)
	}
}

func (m *MutexIndex) groupsOf(qid string) []string {
	out := make([]string, 0, len(m.membership[qid]))
	for g := range m.membership[qid] {
		out = append(out, g)
	}
	return sortedCopy(out)
}

func (m *MutexIndex) joinGroup(qid, g string) {
	members := m.groups[g]
	if members == nil {
		members = map[string]bool{}
		m.groups[g] = members
	}
	if m.adj[qid] == nil {
		m.adj[qid] = map[string]bool{}
	}
	for other := range members {
		m.adj[qid][other] = true
		m.adj[other][qid] = true
	}
	members[qid] = true
	if m.membership[qid] == nil {
		m.membership[qid] = map[string]bool{}
	}
	m.membership[qid][g] = true
}

func (m *MutexIndex) leaveGroup(qid, g string) {
	members := m.groups[g]
	if members == nil || !members[qid] {
		return
	}
	delete(members, qid)
	if len(members) == 0 {
		delete(m.groups, g)
	}
	// 先移除成员关系，再按“是否仍共享其他组”决定是否断开邻接边。
	delete(m.membership[qid], g)
	if len(m.membership[qid]) == 0 {
		delete(m.membership, qid)
	}
	for other := range members {
		if !m.shareAnyGroup(qid, other) {
			delete(m.adj[qid], other)
			delete(m.adj[other], qid)
		}
	}
}

func (m *MutexIndex) shareAnyGroup(a, b string) bool {
	for g := range m.membership[a] {
		if m.membership[b][g] {
			return true
		}
	}
	return false
}

// conflictWithSet 判定候选题 candidate 是否与集合 set 中某题互斥。
// 返回命中的题目与 BFS 访问顶点数（用于复杂度可验证性）。
// 只遍历 candidate 所在连通分量，开销与题目总数、组总数无关。
func (m *MutexIndex) conflictWithSet(candidate string, set map[string]bool) (string, int) {
	visited := map[string]bool{candidate: true}
	queue := []string{candidate}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur != candidate && set[cur] {
			return cur, len(visited)
		}
		for next := range m.adj[cur] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return "", len(visited)
}

// conflictInSet 在题目集合内部寻找一对互斥题目（传递闭包意义下）。
// 两题互斥当且仅当处于同一连通分量，故从集合内某题 BFS 时
// 到达另一集合内题目即构成冲突。
func (m *MutexIndex) conflictInSet(ids []string) (string, string, bool) {
	inSet := map[string]bool{}
	for _, id := range ids {
		inSet[id] = true
	}
	done := map[string]bool{} // 已确认所在分量无冲突的集合内题目
	for _, id := range ids {
		if done[id] {
			continue
		}
		visited := map[string]bool{id: true}
		queue := []string{id}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for next := range m.adj[cur] {
				if visited[next] {
					continue
				}
				visited[next] = true
				if inSet[next] {
					return id, next, true
				}
				queue = append(queue, next)
			}
		}
		done[id] = true
	}
	return "", "", false
}
