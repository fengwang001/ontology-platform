package equivalence

import "sort"

// Equivalent 判断 a 与 b 是否属于同一等价类。
//
// 任一元素未知时返回包装了 ErrUnknownElement 的错误；这与「已知但不连通」
// （返回 (false, nil)）是两种可区分的结果，不得用 false 混淆。
func (u *UnionFind) Equivalent(a, b string) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, ok := u.parent[a]; !ok {
		return false, ErrUnknownElement
	}
	if _, ok := u.parent[b]; !ok {
		return false, ErrUnknownElement
	}
	return u.findLocked(a) == u.findLocked(b), nil
}

// ClassCount 返回当前等价类的个数（单元素类也算一个）。
func (u *UnionFind) ClassCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()

	count := 0
	for id, parent := range u.parent {
		if id == parent {
			count++
		}
	}
	return count
}

// Classes 导出全部等价类。
//
// 外层按代表元字典序排列；每个类内成员按字典序排列。
// 结果与 Union 顺序及内部 map 迭代顺序无关。
func (u *UnionFind) Classes() [][]string {
	u.mu.Lock()
	defer u.mu.Unlock()

	// rep -> members；代表元与内部根解耦，保证结果不受合并顺序影响。
	groups := make(map[string][]string)
	var reps []string
	for id := range u.parent {
		root := u.findLocked(id)
		rep := u.minimum[root]
		if _, seen := groups[rep]; !seen {
			reps = append(reps, rep)
		}
		groups[rep] = append(groups[rep], id)
	}

	sort.Strings(reps)
	result := make([][]string, 0, len(reps))
	for _, rep := range reps {
		members := groups[rep]
		sort.Strings(members)
		result = append(result, members)
	}
	return result
}
