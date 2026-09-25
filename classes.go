package ontology

import (
	"fmt"
	"sort"
)

// Connected 报告 a、b 是否属于同一等价类。
// 任一元素未知时返回 ErrUnknownElement，
// 与「不连通」（false, nil）严格区分。
func (u *UnionFind) Connected(a, b string) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.elems[a]; !ok {
		return false, fmt.Errorf("ontology: Connected(%q, %q): %w", a, b, ErrUnknownElement)
	}
	if _, ok := u.elems[b]; !ok {
		return false, fmt.Errorf("ontology: Connected(%q, %q): %w", a, b, ErrUnknownElement)
	}
	raID, _ := u.findRootLocked(a)
	rbID, _ := u.findRootLocked(b)
	return raID == rbID, nil
}

// ClassCount 返回当前等价类的个数。
func (u *UnionFind) ClassCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.count
}

// Classes 导出全部等价类。输出是确定的：
// 类内成员按字典序排列，类之间按代表元（类内最小 ID）字典序排列，
// 与 Union 顺序及内部 map 迭代顺序无关。
func (u *UnionFind) Classes() [][]string {
	u.mu.Lock()
	defer u.mu.Unlock()
	byRoot := make(map[string][]string)
	for id := range u.elems {
		rootID, _ := u.findRootLocked(id)
		byRoot[rootID] = append(byRoot[rootID], id)
	}
	classes := make([][]string, 0, len(byRoot))
	for _, members := range byRoot {
		sort.Strings(members)
		classes = append(classes, members)
	}
	// 排序后每个类的首元素即代表元（类内最小 ID）。
	sort.Slice(classes, func(i, j int) bool {
		return classes[i][0] < classes[j][0]
	})
	return classes
}
