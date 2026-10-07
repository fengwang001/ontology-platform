package submod

import (
	"sort"
	"strings"
)

// 本文件实现协调器的四类变更操作。所有操作都分两阶段：先按错误次序
// 完成全部校验（任一失败即整体拒绝、什么都不改），再一次落地全部变更，
// 因此一次递归更新要么全部落地要么完全不发生。

// UpdateToPins 按固定更新：把每个挂载点的实际检出提交对齐到固定提交，
// 递归到嵌套子模块。任一挂载点有未提交修改或悬空固定即整体拒绝。
func (c *Coordinator) UpdateToPins() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	type entry struct {
		path string
		rec  Record
	}
	var entries []entry
	// 校验一：循环挂载（walk 沿每条挂载链检测）。
	if err := c.walk(func(p string, rec Record) {
		entries = append(entries, entry{p, rec})
	}); err != nil {
		return c.revision, err
	}
	// 校验二：悬空固定。
	for _, e := range entries {
		repo := c.repos[e.rec.Repo]
		if repo == nil || !repo.hasCommit(e.rec.Commit) {
			return c.revision, errf(ErrCodeDanglingPin, e.path,
				"固定提交 %q 不存在于仓库 %q", e.rec.Commit, e.rec.Repo)
		}
	}
	// 校验三：未提交修改。
	for _, e := range entries {
		if co := c.checkouts[e.path]; co != nil && co.dirty {
			return c.revision, errf(ErrCodeDirtyWorkspace, e.path,
				"挂载点有未提交修改: %s", e.path)
		}
	}
	// 落地：自顶向下（按深度排序）对齐所有检出。
	sort.Slice(entries, func(i, j int) bool {
		di, dj := strings.Count(entries[i].path, "/"), strings.Count(entries[j].path, "/")
		if di != dj {
			return di < dj
		}
		return entries[i].path < entries[j].path
	})
	for _, e := range entries {
		co := c.checkouts[e.path]
		if co == nil {
			co = &checkout{}
			c.checkouts[e.path] = co
		}
		co.commit = e.rec.Commit
	}
	c.revision++
	return c.revision, nil
}

// AdvanceTracking 按跟踪分支推进：对超级仓库子模块表中配置了跟踪分支的
// 每条记录，把固定提交推进到目标仓库该分支的当前顶端，并产生一次超级
// 仓库子模块表变更（新提交 + 分支前移）。整批原子：任何一条被拒，整批不变。
func (c *Coordinator) AdvanceTracking() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 校验一：循环挂载（挂载树被污染时一律拒绝）。
	if err := c.walk(func(string, Record) {}); err != nil {
		return c.revision, err
	}
	table := c.currentTable()
	type adv struct {
		path string
		rec  Record
		tip  string
	}
	var advs []adv
	for _, p := range sortedKeys(table) {
		if rec := table[p]; rec.Track != "" {
			advs = append(advs, adv{path: p, rec: rec})
		}
	}
	if len(advs) == 0 {
		return c.revision, nil // 无跟踪记录：无操作
	}
	// 校验二：仓库不存在。
	for _, a := range advs {
		if c.repos[a.rec.Repo] == nil {
			return c.revision, errf(ErrCodeRepoNotFound, a.path,
				"目标仓库 %q 不存在", a.rec.Repo)
		}
	}
	// 校验三：分支不存在。
	for i := range advs {
		a := &advs[i]
		tip, ok := c.repos[a.rec.Repo].Branches[a.rec.Track]
		if !ok {
			return c.revision, errf(ErrCodeBranchNotFound, a.path,
				"仓库 %q 中分支 %q 不存在", a.rec.Repo, a.rec.Track)
		}
		a.tip = tip
	}
	// 校验四：悬空固定（旧固定点不存在则无法判定祖先关系）。
	for _, a := range advs {
		if !c.repos[a.rec.Repo].hasCommit(a.rec.Commit) {
			return c.revision, errf(ErrCodeDanglingPin, a.path,
				"固定提交 %q 不存在于仓库 %q", a.rec.Commit, a.rec.Repo)
		}
	}
	// 校验五：未提交修改（推进涉及的工作区挂载点必须干净）。
	for _, a := range advs {
		if co := c.checkouts[a.path]; co != nil && co.dirty {
			return c.revision, errf(ErrCodeDirtyWorkspace, a.path,
				"挂载点有未提交修改: %s", a.path)
		}
	}
	// 校验六：跟踪分支非快进（新顶端须为旧固定点的后代，含相等）。
	for _, a := range advs {
		if !isAncestorOrEqual(c.repos[a.rec.Repo], a.rec.Commit, a.tip) {
			return c.revision, errf(ErrCodeNonFastForward, a.path,
				"分支 %q 的顶端 %q 不是固定点 %q 的后代", a.rec.Track, a.tip, a.rec.Commit)
		}
	}
	// 落地：生成一次超级仓库子模块表变更。
	newTable := make(Table, len(table))
	for k, v := range table {
		newTable[k] = v
	}
	for _, a := range advs {
		rec := a.rec
		rec.Commit = a.tip
		newTable[a.path] = rec
	}
	c.commitSuperTable(newTable)
	c.revision++
	return c.revision, nil
}

// commitSuperTable 用新子模块表在超级仓库上产生一个提交并前移分支。
// 调用方须持有锁。
func (c *Coordinator) commitSuperTable(t Table) {
	super := c.repos[c.superID]
	tip := super.Branches[c.superBranch]
	super.Branches[c.superBranch] = super.createCommit([]string{tip}, t)
}

// AddMount 在超级仓库子模块表中添加一条挂载记录，并产生一次表变更。
// 路径冲突、目标仓库不存在、固定提交不存在为可区分错误。
func (c *Coordinator) AddMount(path, repoID, commit, track string) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 校验一：参数非法。
	if path == "" || repoID == "" {
		return c.revision, errf(ErrCodeInvalidParam, path, "空路径或空仓库标识")
	}
	npath, err := NormalizePath(path)
	if err != nil {
		return c.revision, err
	}
	// 校验二：循环挂载（既有树被污染，或新记录落在自己祖先链上）。
	var treePaths []string
	chainRepos := map[string]bool{c.superID: true}
	walkErr := c.walk(func(p string, rec Record) {
		treePaths = append(treePaths, p)
		if isAncestorPath(p, npath) {
			chainRepos[rec.Repo] = true
		}
	})
	if walkErr != nil {
		return c.revision, walkErr
	}
	if chainRepos[repoID] {
		return c.revision, errf(ErrCodeCircularMount, npath,
			"仓库 %q 已在新挂载点的祖先挂载链上", repoID)
	}
	// 校验三：路径冲突（与挂载树中任一路径相同或互为祖先后代）。
	for _, p := range treePaths {
		if p == npath || isAncestorPath(p, npath) || isAncestorPath(npath, p) {
			return c.revision, errf(ErrCodePathConflict, npath,
				"路径 %q 与已有挂载点 %q 冲突", npath, p)
		}
	}
	// 校验四：仓库不存在。
	repo := c.repos[repoID]
	if repo == nil {
		return c.revision, errf(ErrCodeRepoNotFound, npath, "目标仓库 %q 不存在", repoID)
	}
	// 校验五：分支不存在（仅当配置了跟踪分支）。
	if track != "" {
		if _, ok := repo.Branches[track]; !ok {
			return c.revision, errf(ErrCodeBranchNotFound, npath,
				"仓库 %q 中分支 %q 不存在", repoID, track)
		}
	}
	// 校验六：悬空固定。
	if !repo.hasCommit(commit) {
		return c.revision, errf(ErrCodeDanglingPin, npath,
			"固定提交 %q 不存在于仓库 %q", commit, repoID)
	}
	// 落地。
	newTable := make(Table, len(c.currentTable())+1)
	for k, v := range c.currentTable() {
		newTable[k] = v
	}
	newTable[npath] = Record{Repo: repoID, Commit: commit, Track: track}
	c.commitSuperTable(newTable)
	c.revision++
	return c.revision, nil
}

// RemoveMount 从超级仓库子模块表移除一条挂载记录，并产生一次表变更。
// 该挂载点有未提交修改时拒绝，force 为真才允许丢弃。
func (c *Coordinator) RemoveMount(path string, force bool) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 校验一：参数非法。
	if path == "" {
		return c.revision, errf(ErrCodeInvalidParam, path, "空路径")
	}
	npath, err := NormalizePath(path)
	if err != nil {
		return c.revision, err
	}
	// 校验二：循环挂载（挂载树被污染时一律拒绝）。
	if err := c.walk(func(string, Record) {}); err != nil {
		return c.revision, err
	}
	// 校验三：挂载点须存在于超级仓库子模块表。
	table := c.currentTable()
	if _, ok := table[npath]; !ok {
		return c.revision, errf(ErrCodeMountNotFound, npath, "挂载点不存在: %s", npath)
	}
	// 校验四：未提交修改（带强制标志才允许丢弃）。
	if co := c.checkouts[npath]; co != nil && co.dirty && !force {
		return c.revision, errf(ErrCodeDirtyWorkspace, npath,
			"挂载点有未提交修改，需强制标志才能移除: %s", npath)
	}
	// 落地：表变更 + 清理该挂载点子树的检出状态。
	newTable := make(Table, len(table)-1)
	for k, v := range table {
		if k != npath {
			newTable[k] = v
		}
	}
	c.commitSuperTable(newTable)
	for p := range c.checkouts {
		if p == npath || strings.HasPrefix(p, npath+"/") {
			delete(c.checkouts, p)
		}
	}
	c.revision++
	return c.revision, nil
}
