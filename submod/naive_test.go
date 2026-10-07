package submod

// 本文件是独立的朴素对照模型：不调用 Coordinator 的任何方法，
// 用直接的全树递归实现同一套语义，供随机测试与并发回放比对。

import "strings"

type naiveEntry struct {
	path string
	rec  Record
}

type naive struct {
	repos       map[string]*Repo
	superID     string
	superBranch string
	checkouts   map[string]*checkout
	revision    uint64
}

func newNaive(repos map[string]*Repo, superID, superBranch string) *naive {
	return &naive{
		repos:       repos,
		superID:     superID,
		superBranch: superBranch,
		checkouts:   map[string]*checkout{},
	}
}

func (n *naive) table() Table {
	super := n.repos[n.superID]
	return super.Commits[super.Branches[n.superBranch]].Table
}

// collect 全树递归收集挂载点，沿挂载链检测循环。
func (n *naive) collect() ([]naiveEntry, error) {
	var out []naiveEntry
	err := n.collectInto(n.table(), "", []string{n.superID}, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (n *naive) collectInto(t Table, prefix string, chain []string, out *[]naiveEntry) error {
	for _, p := range sortedKeys(t) {
		rec := t[p]
		for _, id := range chain {
			if id == rec.Repo {
				return errf(ErrCodeCircularMount, joinPath(prefix, p),
					"仓库 %q 在自己的祖先挂载链上再次出现", rec.Repo)
			}
		}
		tp := joinPath(prefix, p)
		*out = append(*out, naiveEntry{tp, rec})
		repo := n.repos[rec.Repo]
		if repo == nil {
			continue
		}
		com := repo.Commits[rec.Commit]
		if com == nil {
			continue
		}
		if err := n.collectInto(com.Table, tp, append(chain, rec.Repo), out); err != nil {
			return err
		}
	}
	return nil
}

func (n *naive) statusOf(path string, rec Record) Status {
	repo := n.repos[rec.Repo]
	if repo == nil || !repo.hasCommit(rec.Commit) {
		return StatusDangling
	}
	co := n.checkouts[path]
	if co == nil {
		return StatusMissing
	}
	if co.dirty {
		return StatusDirty
	}
	if co.commit != rec.Commit {
		return StatusDiverged
	}
	return StatusConsistent
}

func (n *naive) statusAll() (map[string]Status, error) {
	entries, err := n.collect()
	if err != nil {
		return nil, err
	}
	out := map[string]Status{}
	for _, e := range entries {
		out[e.path] = n.statusOf(e.path, e.rec)
	}
	return out, nil
}

// resolve 只沿目标路径所在挂载链解析（与协调器语义一致）。
func (n *naive) resolve(path string) (Record, error) {
	chain := []string{n.superID}
	table := n.table()
	remaining := path
	for {
		key, rec, ok := findRecord(table, remaining)
		if !ok {
			return Record{}, errf(ErrCodeMountNotFound, path, "挂载点不存在: %s", path)
		}
		for _, id := range chain {
			if id == rec.Repo {
				return Record{}, errf(ErrCodeCircularMount, path,
					"仓库 %q 在自己的祖先挂载链上再次出现", rec.Repo)
			}
		}
		if key == remaining {
			return rec, nil
		}
		repo := n.repos[rec.Repo]
		if repo == nil || repo.Commits[rec.Commit] == nil {
			return Record{}, errf(ErrCodeMountNotFound, path, "挂载点不存在（祖先固定悬空）: %s", path)
		}
		chain = append(chain, rec.Repo)
		table = repo.Commits[rec.Commit].Table
		remaining = remaining[len(key)+1:]
	}
}

func (n *naive) statusAt(path string) (Status, error) {
	npath, err := NormalizePath(path)
	if err != nil {
		return StatusConsistent, err
	}
	rec, err := n.resolve(npath)
	if err != nil {
		return StatusConsistent, err
	}
	return n.statusOf(npath, rec), nil
}

func (n *naive) update() (uint64, error) {
	entries, err := n.collect()
	if err != nil {
		return n.revision, err
	}
	for _, e := range entries {
		repo := n.repos[e.rec.Repo]
		if repo == nil || !repo.hasCommit(e.rec.Commit) {
			return n.revision, errf(ErrCodeDanglingPin, e.path,
				"固定提交 %q 不存在于仓库 %q", e.rec.Commit, e.rec.Repo)
		}
	}
	for _, e := range entries {
		if co := n.checkouts[e.path]; co != nil && co.dirty {
			return n.revision, errf(ErrCodeDirtyWorkspace, e.path,
				"挂载点有未提交修改: %s", e.path)
		}
	}
	for _, e := range entries {
		co := n.checkouts[e.path]
		if co == nil {
			co = &checkout{}
			n.checkouts[e.path] = co
		}
		co.commit = e.rec.Commit
	}
	n.revision++
	return n.revision, nil
}

func (n *naive) advance() (uint64, error) {
	if _, err := n.collect(); err != nil {
		return n.revision, err
	}
	type adv struct {
		path string
		rec  Record
		tip  string
	}
	var advs []adv
	for _, p := range sortedKeys(n.table()) {
		if rec := n.table()[p]; rec.Track != "" {
			advs = append(advs, adv{path: p, rec: rec})
		}
	}
	if len(advs) == 0 {
		return n.revision, nil
	}
	for _, a := range advs {
		if n.repos[a.rec.Repo] == nil {
			return n.revision, errf(ErrCodeRepoNotFound, a.path, "目标仓库 %q 不存在", a.rec.Repo)
		}
	}
	for i := range advs {
		a := &advs[i]
		tip, ok := n.repos[a.rec.Repo].Branches[a.rec.Track]
		if !ok {
			return n.revision, errf(ErrCodeBranchNotFound, a.path,
				"仓库 %q 中分支 %q 不存在", a.rec.Repo, a.rec.Track)
		}
		a.tip = tip
	}
	for _, a := range advs {
		if !n.repos[a.rec.Repo].hasCommit(a.rec.Commit) {
			return n.revision, errf(ErrCodeDanglingPin, a.path,
				"固定提交 %q 不存在于仓库 %q", a.rec.Commit, a.rec.Repo)
		}
	}
	for _, a := range advs {
		if co := n.checkouts[a.path]; co != nil && co.dirty {
			return n.revision, errf(ErrCodeDirtyWorkspace, a.path,
				"挂载点有未提交修改: %s", a.path)
		}
	}
	for _, a := range advs {
		if !isAncestorOrEqual(n.repos[a.rec.Repo], a.rec.Commit, a.tip) {
			return n.revision, errf(ErrCodeNonFastForward, a.path,
				"分支 %q 的顶端 %q 不是固定点 %q 的后代", a.rec.Track, a.tip, a.rec.Commit)
		}
	}
	newTable := make(Table, len(n.table()))
	for k, v := range n.table() {
		newTable[k] = v
	}
	for _, a := range advs {
		rec := a.rec
		rec.Commit = a.tip
		newTable[a.path] = rec
	}
	n.commitSuperTable(newTable)
	n.revision++
	return n.revision, nil
}

func (n *naive) commitSuperTable(t Table) {
	super := n.repos[n.superID]
	tip := super.Branches[n.superBranch]
	super.Branches[n.superBranch] = super.createCommit([]string{tip}, t)
}

func (n *naive) add(path, repoID, commit, track string) (uint64, error) {
	if path == "" || repoID == "" {
		return n.revision, errf(ErrCodeInvalidParam, path, "空路径或空仓库标识")
	}
	npath, err := NormalizePath(path)
	if err != nil {
		return n.revision, err
	}
	entries, err := n.collect()
	if err != nil {
		return n.revision, err
	}
	chainRepos := map[string]bool{n.superID: true}
	for _, e := range entries {
		if isAncestorPath(e.path, npath) {
			chainRepos[e.rec.Repo] = true
		}
	}
	if chainRepos[repoID] {
		return n.revision, errf(ErrCodeCircularMount, npath,
			"仓库 %q 已在新挂载点的祖先挂载链上", repoID)
	}
	for _, e := range entries {
		if e.path == npath || isAncestorPath(e.path, npath) || isAncestorPath(npath, e.path) {
			return n.revision, errf(ErrCodePathConflict, npath,
				"路径 %q 与已有挂载点 %q 冲突", npath, e.path)
		}
	}
	repo := n.repos[repoID]
	if repo == nil {
		return n.revision, errf(ErrCodeRepoNotFound, npath, "目标仓库 %q 不存在", repoID)
	}
	if track != "" {
		if _, ok := repo.Branches[track]; !ok {
			return n.revision, errf(ErrCodeBranchNotFound, npath,
				"仓库 %q 中分支 %q 不存在", repoID, track)
		}
	}
	if !repo.hasCommit(commit) {
		return n.revision, errf(ErrCodeDanglingPin, npath,
			"固定提交 %q 不存在于仓库 %q", commit, repoID)
	}
	newTable := make(Table, len(n.table())+1)
	for k, v := range n.table() {
		newTable[k] = v
	}
	newTable[npath] = Record{Repo: repoID, Commit: commit, Track: track}
	n.commitSuperTable(newTable)
	n.revision++
	return n.revision, nil
}

func (n *naive) remove(path string, force bool) (uint64, error) {
	if path == "" {
		return n.revision, errf(ErrCodeInvalidParam, path, "空路径")
	}
	npath, err := NormalizePath(path)
	if err != nil {
		return n.revision, err
	}
	if _, err := n.collect(); err != nil {
		return n.revision, err
	}
	if _, ok := n.table()[npath]; !ok {
		return n.revision, errf(ErrCodeMountNotFound, npath, "挂载点不存在: %s", npath)
	}
	if co := n.checkouts[npath]; co != nil && co.dirty && !force {
		return n.revision, errf(ErrCodeDirtyWorkspace, npath,
			"挂载点有未提交修改，需强制标志才能移除: %s", npath)
	}
	newTable := make(Table, len(n.table()))
	for k, v := range n.table() {
		if k != npath {
			newTable[k] = v
		}
	}
	n.commitSuperTable(newTable)
	for p := range n.checkouts {
		if p == npath || strings.HasPrefix(p, npath+"/") {
			delete(n.checkouts, p)
		}
	}
	n.revision++
	return n.revision, nil
}

func (n *naive) setCheckout(path, commit string) (uint64, error) {
	npath, err := NormalizePath(path)
	if err != nil {
		return n.revision, err
	}
	co := n.checkouts[npath]
	if co == nil {
		co = &checkout{}
		n.checkouts[npath] = co
	}
	co.commit = commit
	n.revision++
	return n.revision, nil
}

func (n *naive) setDirty(path string, dirty bool) (uint64, error) {
	npath, err := NormalizePath(path)
	if err != nil {
		return n.revision, err
	}
	co := n.checkouts[npath]
	if co == nil {
		return n.revision, errf(ErrCodeMountNotFound, npath, "无检出，无法标记未提交修改: %s", npath)
	}
	co.dirty = dirty
	n.revision++
	return n.revision, nil
}

func (n *naive) clearCheckout(path string) (uint64, error) {
	npath, err := NormalizePath(path)
	if err != nil {
		return n.revision, err
	}
	delete(n.checkouts, npath)
	n.revision++
	return n.revision, nil
}

// cloneRepos 深拷贝仓库集合，使协调器与朴素模型互不干扰。
func cloneRepos(repos map[string]*Repo) map[string]*Repo {
	out := make(map[string]*Repo, len(repos))
	for id, r := range repos {
		nr := NewRepo(id)
		nr.autoN = r.autoN
		for cid, com := range r.Commits {
			var tab Table
			if com.Table != nil {
				tab = make(Table, len(com.Table))
				for k, v := range com.Table {
					tab[k] = v
				}
			}
			nr.Commits[cid] = &Commit{ID: com.ID, Parents: append([]string(nil), com.Parents...), Table: tab}
		}
		for b, tip := range r.Branches {
			nr.Branches[b] = tip
		}
		out[id] = nr
	}
	return out
}
