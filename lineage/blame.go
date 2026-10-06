package lineage

import "sync"

type fileKey struct {
	commit string
	path   string
}

type queryKey struct {
	commit  string
	path    string
	version int
}

// rawAttr 是不考虑忽略名单的「原始归属」：行内容由哪个提交首次引入。
type rawAttr struct {
	commit string
	path   string
	line   int
}

type fileBlame struct {
	attrs []rawAttr
}

// blameEntry 每个 (提交, 路径) 一份，sync.Once 保证只解析一次。
// 若本提交该路径内容与第一父完全一致且未改名，jump 直接指向第一父的
// 规范条目（路径压缩），否则 fb 为本地计算结果。
type blameEntry struct {
	once sync.Once
	jump *fileKey
	fb   *fileBlame
}

// Blame 按（提交标识、路径、名单版本）返回每一行的归属。
// 错误只报次序中最靠前的一个：参数非法 > 提交不存在 > 名单版本不存在 > 路径不存在。
// 被拒绝的查询不触碰任何缓存与统计。
func (s *Service) Blame(commitID, path string, version int) ([]Attribution, error) {
	if commitID == "" || path == "" || version < 0 {
		return nil, newError(KindInvalidParam, "empty commit id/path or negative version")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.commits[commitID]
	if !ok {
		return nil, newError(KindCommitNotFound, "commit %q not found", commitID)
	}
	if version >= len(s.ignoreVersions) {
		return nil, newError(KindVersionNotFound, "ignore version %d not found", version)
	}
	if _, ok := c.Files[path]; !ok {
		return nil, newError(KindPathNotFound, "path %q not in commit %q", path, commitID)
	}
	qk := queryKey{commit: commitID, path: path, version: version}
	s.qcacheMu.Lock()
	cached, hit := s.queryCache[qk]
	s.qcacheMu.Unlock()
	if hit {
		s.qhits.Add(1)
		return append([]Attribution(nil), cached...), nil
	}
	fb := s.rawBlame(commitID, path)
	ignore := s.ignoreVersions[version]
	res := make([]Attribution, len(fb.attrs))
	for i, ra := range fb.attrs {
		// 追溯链与忽略名单无关：中间被忽略的提交本来就会被穿透，
		// 只有最终引入者落在名单里时结果保留并打标记。
		res[i] = Attribution{
			CommitID:             ra.commit,
			Path:                 ra.path,
			Line:                 ra.line,
			IgnoredButAttributed: ignore[ra.commit],
		}
	}
	s.qcacheMu.Lock()
	s.queryCache[qk] = res
	s.qcacheMu.Unlock()
	return append([]Attribution(nil), res...), nil
}

func (s *Service) entry(k fileKey) *blameEntry {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	e, ok := s.blameCache[k]
	if !ok {
		e = &blameEntry{}
		s.blameCache[k] = e
	}
	return e
}

// canonical 返回 (提交, 路径) 的规范条目键：沿「内容与第一父一致且未改名」
// 的链跳到真正发生变化的祖先，跳跃目标直接指向链尾（路径压缩）。
func (s *Service) canonical(id, path string) fileKey {
	k := fileKey{commit: id, path: path}
	e := s.entry(k)
	e.once.Do(func() { s.resolve(e, k) })
	if e.jump != nil {
		return *e.jump
	}
	return k
}

func (s *Service) resolve(e *blameEntry, k fileKey) {
	c := s.commits[k.commit]
	renamed := false
	for _, r := range c.Renames {
		if r.New == k.path {
			renamed = true
			break
		}
	}
	if !renamed && len(c.Parents) > 0 {
		if pc, ok := s.commits[c.Parents[0]].Files[k.path]; ok && pc == c.Files[k.path] {
			j := s.canonical(c.Parents[0], k.path)
			e.jump = &j
			return
		}
	}
	e.fb = s.computeBlame(k.commit, k.path)
}

func (s *Service) rawBlame(id, path string) *fileBlame {
	k := s.canonical(id, path)
	return s.entry(k).fb
}

// computeBlame 计算一个提交中一个路径每行的原始归属。
// 只访问该路径改名链上的祖先，与无关提交数无关。
func (s *Service) computeBlame(id, path string) *fileBlame {
	s.fullComp.Add(1)
	c := s.commits[id]
	lines := splitLines(c.Files[path])
	attrs := make([]rawAttr, len(lines))

	parentPath := path
	for _, r := range c.Renames {
		if r.New == path {
			parentPath = r.Old
			break
		}
	}
	// 每个父提交各自做一次行对应；父中不存在对应路径则该父无可配对行。
	matches := make([][]int, len(c.Parents))
	for pi, pid := range c.Parents {
		pc, ok := s.commits[pid].Files[parentPath]
		if !ok {
			continue
		}
		s.aligns.Add(1)
		matches[pi] = align(splitLines(pc), lines)
	}
	for i := range lines {
		chosen := -1
		for pi := range c.Parents {
			if matches[pi] != nil && matches[pi][i] >= 0 {
				chosen = pi // 第一父优先，其次序号最小者
				break
			}
		}
		if chosen < 0 {
			attrs[i] = rawAttr{commit: id, path: path, line: i + 1}
		} else {
			pf := s.rawBlame(c.Parents[chosen], parentPath)
			attrs[i] = pf.attrs[matches[chosen][i]]
		}
	}
	return &fileBlame{attrs: attrs}
}
