package lineage

import (
	"sync"
	"sync/atomic"
)

// Service 是面向提交图的行归属追溯服务。
// 载入、名单变更与查询可任意并发，效果等价于某个串行顺序。
type Service struct {
	mu             sync.RWMutex // 保护 commits 与 ignoreVersions；查询全程持读锁，保证看不到「载入一半」
	commits        map[string]*Commit
	ignoreVersions []map[string]bool

	cacheMu    sync.Mutex // 保护 blameCache
	blameCache map[fileKey]*blameEntry
	qcacheMu   sync.Mutex // 保护 queryCache
	queryCache map[queryKey][]Attribution

	aligns   atomic.Int64 // 对齐计算次数（性能验证用）
	fullComp atomic.Int64 // 完整归属计算次数（性能验证用）
	qhits    atomic.Int64 // 查询缓存命中次数
}

// Stats 是内部工作量的可验证计数快照。
type Stats struct {
	AlignComputations     int64
	FullBlameComputations int64
	QueryCacheHits        int64
}

func NewService() *Service {
	return &Service{
		commits:    make(map[string]*Commit),
		blameCache: make(map[fileKey]*blameEntry),
		queryCache: make(map[queryKey][]Attribution),
	}
}

// Stats 返回当前计数快照。
func (s *Service) Stats() Stats {
	return Stats{
		AlignComputations:     s.aligns.Load(),
		FullBlameComputations: s.fullComp.Load(),
		QueryCacheHits:        s.qhits.Load(),
	}
}

// CommitCount 返回已载入提交数（测试与监控用）。
func (s *Service) CommitCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.commits)
}

// LoadCommit 载入一个提交。任何校验失败都拒绝且不留痕迹。
func (s *Service) LoadCommit(c Commit) error {
	if c.ID == "" {
		return newError(KindInvalidParam, "commit id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.commits[c.ID]; dup {
		return newError(KindDuplicateCommit, "commit %q already loaded", c.ID)
	}
	for _, p := range c.Parents {
		if _, ok := s.commits[p]; !ok {
			return newError(KindParentNotFound, "parent %q of commit %q not found", p, c.ID)
		}
	}
	seenNew := make(map[string]bool, len(c.Renames))
	for _, r := range c.Renames {
		if r.Old == "" || r.New == "" {
			return newError(KindInvalidParam, "commit %q has rename with empty path", c.ID)
		}
		if len(c.Parents) == 0 {
			return newError(KindInvalidRename, "commit %q renames %q but has no parent", c.ID, r.Old)
		}
		first := s.commits[c.Parents[0]]
		if _, ok := first.Files[r.Old]; !ok {
			return newError(KindInvalidRename, "rename old path %q not in first parent of %q", r.Old, c.ID)
		}
		if _, ok := c.Files[r.Old]; ok {
			return newError(KindInvalidRename, "rename old path %q still present in commit %q", r.Old, c.ID)
		}
		if seenNew[r.New] {
			return newError(KindInvalidRename, "commit %q has multiple renames to %q", c.ID, r.New)
		}
		seenNew[r.New] = true
	}
	// 深拷贝，保证已载入提交永远不可修改。
	files := make(map[string]string, len(c.Files))
	for k, v := range c.Files {
		files[k] = v
	}
	parents := append([]string(nil), c.Parents...)
	renames := append([]Rename(nil), c.Renames...)
	s.commits[c.ID] = &Commit{ID: c.ID, Parents: parents, Files: files, Renames: renames}
	return nil
}

// NewIgnoreVersion 以给定提交标识集合创建新的忽略名单版本，返回版本号（从 0 递增）。
// 旧版本永远可查询。名单中含有不存在的提交标识视为参数非法。
func (s *Service) NewIgnoreVersion(ids []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if id == "" {
			return -1, newError(KindInvalidParam, "ignore list contains empty commit id")
		}
		if _, ok := s.commits[id]; !ok {
			return -1, newError(KindInvalidParam, "ignore list contains unknown commit %q", id)
		}
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	s.ignoreVersions = append(s.ignoreVersions, set)
	return len(s.ignoreVersions) - 1, nil
}
