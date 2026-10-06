package blame

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Stats 是服务的运行统计，用于以可验证的方式证明性能性质。
// 被拒绝的载入与查询不影响任何计数。
type Stats struct {
	CommitsLoaded      int64 // 成功载入的提交数
	IgnoreVersions     int64 // 已创建的名单版本数（不含初始空版本 0）
	AcceptedQueries    int64 // 通过全部校验的查询数
	BlameUnitsComputed int64 // 实际计算的 (提交, 路径, 名单版本) 归属单元数
	AlignmentsComputed int64 // 实际计算的行对齐次数
	BlameCacheHits     int64 // 归属缓存命中次数
}

// commitNode 是已载入提交的不可变内部表示。
type commitNode struct {
	id      string
	parents []*commitNode
	lines   map[string][]string
	renames map[string]string // newPath -> oldPath
}

// Service 是行归属追溯服务。所有方法可任意并发调用，
// 任何并发执行都等价于这些调用的某一个串行顺序。
type Service struct {
	mu       sync.RWMutex
	commits  map[string]*commitNode
	versions []map[string]bool // versions[0] 为初始空名单

	cacheMu    sync.Mutex
	blameCache map[blameKey][]Attribution
	alignCache map[alignKey][]int

	stats struct {
		commitsLoaded      atomic.Int64
		ignoreVersions     atomic.Int64
		acceptedQueries    atomic.Int64
		blameUnitsComputed atomic.Int64
		alignmentsComputed atomic.Int64
		blameCacheHits     atomic.Int64
	}
}

// NewService 创建空服务，名单版本 0 为初始空名单。
func NewService() *Service {
	return &Service{
		commits:    make(map[string]*commitNode),
		versions:   []map[string]bool{{}},
		blameCache: make(map[blameKey][]Attribution),
		alignCache: make(map[alignKey][]int),
	}
}

// LoadCommit 载入一个提交。校验全部通过才写入，被拒绝时不留任何痕迹。
func (s *Service) LoadCommit(c Commit) error {
	if c.ID == "" {
		return newError(KindInvalidParam, "提交标识为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.commits[c.ID]; dup {
		return newError(KindDuplicateCommit, fmt.Sprintf("提交 %q 已载入", c.ID))
	}
	parents := make([]*commitNode, len(c.Parents))
	for i, pid := range c.Parents {
		p, ok := s.commits[pid]
		if !ok {
			return newError(KindParentNotFound, fmt.Sprintf("提交 %q 的父提交 %q 不存在", c.ID, pid))
		}
		parents[i] = p
	}
	renames := make(map[string]string, len(c.Renames))
	for _, r := range c.Renames {
		if r.OldPath == "" || r.NewPath == "" {
			return newError(KindInvalidRename, "改名记录的路径为空")
		}
		if _, dup := renames[r.NewPath]; dup {
			return newError(KindInvalidRename, fmt.Sprintf("新路径 %q 对应多条改名记录", r.NewPath))
		}
		if len(parents) == 0 {
			return newError(KindInvalidRename, fmt.Sprintf("提交 %q 无父提交，不能声明改名 %q", c.ID, r.OldPath))
		}
		if _, ok := parents[0].lines[r.OldPath]; !ok {
			return newError(KindInvalidRename, fmt.Sprintf("旧路径 %q 在第一父提交中不存在", r.OldPath))
		}
		if _, ok := c.Files[r.OldPath]; ok {
			return newError(KindInvalidRename, fmt.Sprintf("旧路径 %q 在本提交中仍然存在", r.OldPath))
		}
		renames[r.NewPath] = r.OldPath
	}
	lines := make(map[string][]string, len(c.Files))
	for p, content := range c.Files {
		if p == "" {
			return newError(KindInvalidParam, "文件路径为空")
		}
		lines[p] = splitLines(content)
	}

	s.commits[c.ID] = &commitNode{id: c.ID, parents: parents, lines: lines, renames: renames}
	s.stats.commitsLoaded.Add(1)
	return nil
}

// SetIgnoreList 以给定提交标识集合替换忽略名单，产生并返回新版本号。
// 名单中包含不存在的提交标识时视为参数非法，不产生新版本。
func (s *Service) SetIgnoreList(ids []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return 0, newError(KindInvalidParam, "忽略名单包含空标识")
		}
		if _, ok := s.commits[id]; !ok {
			return 0, newError(KindInvalidParam, fmt.Sprintf("忽略名单包含不存在的提交 %q", id))
		}
		set[id] = true
	}
	s.versions = append(s.versions, set)
	s.stats.ignoreVersions.Add(1)
	return len(s.versions) - 1, nil
}

// QueryBlame 按（提交标识、路径、名单版本）返回每一行的归属。
// 错误按 参数非法、提交不存在、名单版本不存在、路径不存在 的次序只报最靠前的一个；
// 被拒绝的查询不影响任何缓存或统计。
func (s *Service) QueryBlame(commitID, path string, version int) ([]Attribution, error) {
	if commitID == "" || path == "" || version < 0 {
		return nil, newError(KindInvalidParam, "空提交标识、空路径或负的名单版本")
	}
	s.mu.RLock()
	node, ok := s.commits[commitID]
	if !ok {
		s.mu.RUnlock()
		return nil, newError(KindCommitNotFound, fmt.Sprintf("提交 %q 不存在", commitID))
	}
	if version >= len(s.versions) {
		s.mu.RUnlock()
		return nil, newError(KindVersionNotFound, fmt.Sprintf("名单版本 %d 不存在", version))
	}
	if _, ok := node.lines[path]; !ok {
		s.mu.RUnlock()
		return nil, newError(KindPathNotFound, fmt.Sprintf("路径 %q 在提交 %q 中不存在", path, commitID))
	}
	ignore := s.versions[version]
	s.mu.RUnlock()

	s.stats.acceptedQueries.Add(1)
	res := s.blameOf(node, path, version, ignore)
	out := make([]Attribution, len(res))
	copy(out, res)
	return out, nil
}

// Stats 返回当前统计快照。
func (s *Service) Stats() Stats {
	return Stats{
		CommitsLoaded:      s.stats.commitsLoaded.Load(),
		IgnoreVersions:     s.stats.ignoreVersions.Load(),
		AcceptedQueries:    s.stats.acceptedQueries.Load(),
		BlameUnitsComputed: s.stats.blameUnitsComputed.Load(),
		AlignmentsComputed: s.stats.alignmentsComputed.Load(),
		BlameCacheHits:     s.stats.blameCacheHits.Load(),
	}
}
