package submod

import "fmt"

// CommitID 提交唯一标识。
type CommitID string

// RepoID 仓库唯一标识。
type RepoID string

// SubmoduleRecord 子模块表中的一条记录。
type SubmoduleRecord struct {
	Path     string   // 规范化后的挂载路径（相对所属仓库根）
	Repo     RepoID   // 目标仓库
	Pinned   CommitID // 固定提交
	Tracking string   // 可选跟踪分支，空串表示不跟踪
}

// Table 子模块表：规范化路径 -> 记录。用 NewTable 构造以保证合法性。
type Table map[string]SubmoduleRecord

// NewTable 校验并构造子模块表：路径须规范化后合法且互不冲突。
// 冲突检查用路径集合与前缀集合完成，开销与路径段数成正比而非记录数平方。
func NewTable(records ...SubmoduleRecord) (Table, error) {
	t := make(Table, len(records))
	prefixes := make(map[string]bool, len(records))
	for _, r := range records {
		if r.Repo == "" {
			return nil, fmt.Errorf("%w: empty repo id for path %q", ErrInvalidParam, r.Path)
		}
		np, err := NormalizePath(r.Path)
		if err != nil {
			return nil, err
		}
		r.Path = np
		if _, dup := t[np]; dup {
			return nil, fmt.Errorf("%w: duplicate %q", ErrPathConflict, np)
		}
		if prefixes[np] {
			return nil, fmt.Errorf("%w: %q is an ancestor of an existing record", ErrPathConflict, np)
		}
		for i := 0; i < len(np); i++ {
			if np[i] == '/' {
				prefix := np[:i]
				if _, ok := t[prefix]; ok {
					return nil, fmt.Errorf("%w: %q vs %q", ErrPathConflict, prefix, np)
				}
			}
		}
		for i := 0; i < len(np); i++ {
			if np[i] == '/' {
				prefixes[np[:i]] = true
			}
		}
		t[np] = r
	}
	return t, nil
}

// Commit 提交：唯一标识、若干父提交、附带的子模块表（可为 nil）。
type Commit struct {
	ID      CommitID
	Parents []CommitID
	Table   Table
}

// Repo 仓库：提交图 + 分支表。
type Repo struct {
	ID       RepoID
	Commits  map[CommitID]*Commit
	Branches map[string]CommitID
}

// NewRepo 创建空仓库。
func NewRepo(id RepoID) *Repo {
	return &Repo{ID: id, Commits: map[CommitID]*Commit{}, Branches: map[string]CommitID{}}
}

// AddCommit 登记提交。
func (r *Repo) AddCommit(c *Commit) { r.Commits[c.ID] = c }

// HasCommit 报告提交是否存在。
func (r *Repo) HasCommit(id CommitID) bool {
	_, ok := r.Commits[id]
	return ok
}

// Tip 返回分支顶端。
func (r *Repo) Tip(branch string) (CommitID, bool) {
	tip, ok := r.Branches[branch]
	return tip, ok
}

// IsDescendantOrEqual 报告 tip 是否等于 anc 或为 anc 的后代（沿父链可达）。
func (r *Repo) IsDescendantOrEqual(tip, anc CommitID) bool {
	seen := map[CommitID]bool{}
	queue := []CommitID{tip}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == anc {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		c, ok := r.Commits[cur]
		if !ok {
			continue
		}
		queue = append(queue, c.Parents...)
	}
	return false
}

// Store 仓库注册表。
type Store struct {
	repos map[RepoID]*Repo
}

// NewStore 创建空注册表。
func NewStore() *Store { return &Store{repos: map[RepoID]*Repo{}} }

// Add 注册仓库。
func (s *Store) Add(r *Repo) { s.repos[r.ID] = r }

// Get 按标识取仓库，不存在返回 nil。
func (s *Store) Get(id RepoID) *Repo { return s.repos[id] }
