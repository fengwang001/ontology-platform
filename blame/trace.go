package blame

// blameKey 是归属结果的缓存键：完整查询三元组。
type blameKey struct {
	commitID string
	path     string
	version  int
}

// alignKey 是行对齐结果的缓存键。对齐只取决于相邻两个提交与路径，
// 与名单版本无关，因此跨版本共享。parentIdx 用于区分同一提交的不同父。
type alignKey struct {
	commitID  string
	parentIdx int
	path      string
}

// blameOf 计算 (node, path, version) 的整文件归属向量。
// 只访问该路径血缘上的提交，与版本库中其它提交的数量无关；
// 结果整体缓存，重复查询的开销不随历史深度增长。
func (s *Service) blameOf(node *commitNode, path string, version int, ignore map[string]bool) []Attribution {
	key := blameKey{commitID: node.id, path: path, version: version}
	s.cacheMu.Lock()
	hit, ok := s.blameCache[key]
	s.cacheMu.Unlock()
	if ok {
		s.stats.blameCacheHits.Add(1)
		return hit
	}
	s.stats.blameUnitsComputed.Add(1)

	lines := node.lines[path]
	pairs := make([][]int, len(node.parents))
	parentPaths := make([]string, len(node.parents))
	for idx, parent := range node.parents {
		pp := path
		if old, renamed := node.renames[path]; renamed {
			pp = old
		}
		parentPaths[idx] = pp
		pairs[idx] = s.alignmentOf(node, idx, parent, path, pp, lines)
	}

	res := make([]Attribution, len(lines))
	parentBlame := make([][]Attribution, len(node.parents))
	ignored := ignore[node.id]
	for l := range lines {
		chosen := -1
		for idx := range node.parents {
			if pairs[idx] != nil && pairs[idx][l] >= 0 {
				chosen = idx
				break
			}
		}
		if chosen < 0 {
			// 所有父提交的对应路径中都没有配对行：归属于本提交。
			// 若本提交被忽略，归属仍落在本提交，但带「被忽略仍归属」标记。
			res[l] = Attribution{CommitID: node.id, Path: path, Line: l + 1, IgnoredButAttributed: ignored}
			continue
		}
		// 归属延续到序号最小的含有配对行的父提交；被忽略的提交
		// 不作为归属结果，递归自然穿透连续的被忽略提交。
		if parentBlame[chosen] == nil {
			parentBlame[chosen] = s.blameOf(node.parents[chosen], parentPaths[chosen], version, ignore)
		}
		res[l] = parentBlame[chosen][pairs[chosen][l]]
	}

	s.cacheMu.Lock()
	s.blameCache[key] = res
	s.cacheMu.Unlock()
	return res
}

// alignmentOf 返回 child 第 path 路径的每一行在第 parentIdx 个父提交的
// 对应路径 pp 中配对到的行号；父提交不含 pp 时返回 nil（无配对）。
func (s *Service) alignmentOf(child *commitNode, parentIdx int, parent *commitNode, path, pp string, childLines []string) []int {
	key := alignKey{commitID: child.id, parentIdx: parentIdx, path: path}
	s.cacheMu.Lock()
	cached, ok := s.alignCache[key]
	s.cacheMu.Unlock()
	if ok {
		return cached
	}

	var pairs []int
	if parentLines, ok := parent.lines[pp]; ok {
		pairs = alignLines(childLines, parentLines)
		s.stats.alignmentsComputed.Add(1)
	}
	s.cacheMu.Lock()
	s.alignCache[key] = pairs
	s.cacheMu.Unlock()
	return pairs
}
