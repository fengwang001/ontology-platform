package equivalence

// Union 把 a 与 b 并入同一等价类；未知 ID 会被隐式创建。
//
// 返回合并后该类的代表元（类中字典序最小的 ID）。merged 为 false
// 表示二者本就同类，此次调用未改变任何结构（代表元、类数均不变）。
func (u *UnionFind) Union(a, b string) (rep string, merged bool, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.addLocked(a)
	u.addLocked(b)

	rootA := u.findLocked(a)
	rootB := u.findLocked(b)
	if rootA == rootB {
		return u.minimum[rootA], false, nil
	}

	// 按秩合并：秩小的根挂到秩大的根下；秩相等时挂到 rootB 上并加秩。
	switch {
	case u.rank[rootA] > u.rank[rootB]:
		rootA, rootB = rootB, rootA
	case u.rank[rootA] == u.rank[rootB]:
		u.rank[rootB]++
	}
	u.parent[rootA] = rootB

	// 合并两个类的最小 ID 记录，保证代表元始终是全局最小。
	if u.minimum[rootA] < u.minimum[rootB] {
		u.minimum[rootB] = u.minimum[rootA]
	}
	delete(u.minimum, rootA)
	delete(u.rank, rootA)

	return u.minimum[rootB], true, nil
}
