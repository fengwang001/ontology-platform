package gate

import "ontology/review"

// T 返回当前目标分支版本（等于已成功合并的请求数）。
func (g *Gate) T() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.t
}

// BaseHead 返回 PR 的基线与当前 head；PR 不存在时 ok 为 false。
func (g *Gate) BaseHead(id int) (base, head int, merged, draft bool, ok bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, exists := g.prs[id]
	if !exists {
		return 0, 0, false, false, false
	}
	return p.base, p.head, p.merged, p.draft, true
}

// Files 返回 PR 当前文件集合的有序拷贝。
func (g *Gate) Files(id int) ([]string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.prs[id]
	if !ok {
		return nil, false
	}
	return append([]string(nil), p.files...), true
}

// Verdict 返回某 PR 某评审人的裁决与是否存在。
func (g *Gate) Verdict(id int, user string) (review.Verdict, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.prs[id]; !ok {
		return 0, false
	}
	return g.store.Verdict(id, user)
}

// Check 返回某 PR 某 (check,head) 的存档状态与是否存在。
func (g *Gate) Check(id int, check string, head int) (review.Status, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.prs[id]; !ok {
		return 0, false
	}
	return g.store.Check(id, check, head)
}

// Touched 返回底层账累计的 Review/Report 记录读写计数。
func (g *Gate) Touched() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.store.Touched
}
