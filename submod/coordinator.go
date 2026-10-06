package submod

import (
	"fmt"
	"strings"
	"sync"
)

// Status 挂载点状态，五种含义互斥。
type Status int

const (
	// StatusClean 一致：实际检出提交等于固定提交。
	StatusClean Status = iota
	// StatusDiverged 检出提交偏离固定点。
	StatusDiverged
	// StatusDirty 有未提交修改（与偏离同时存在时优先报此状态）。
	StatusDirty
	// StatusDangling 悬空固定：固定提交不存在于目标仓库。
	StatusDangling
	// StatusMissing 缺失：挂载路径上没有检出。
	StatusMissing
)

func (s Status) String() string {
	switch s {
	case StatusClean:
		return "clean"
	case StatusDiverged:
		return "diverged"
	case StatusDirty:
		return "dirty"
	case StatusDangling:
		return "dangling"
	case StatusMissing:
		return "missing"
	}
	return "unknown"
}

// Worktree 挂载点的工作区状态。
type Worktree struct {
	Checkout CommitID // 实际检出提交
	Dirty    bool     // 是否有未提交修改
}

// Coordinator 子模块固定与更新协调器。所有公开方法可并发调用，
// 效果等价于某个串行顺序（内部以互斥锁串行化）。
type Coordinator struct {
	mu    sync.RWMutex
	store *Store
	super RepoID
	head  CommitID // 超级仓库当前提交
	gen   uint64   // 变更序号，仅在被接受的变更上递增
	seq   uint64   // 超级仓库新提交标识分配器，仅在被接受的变更上递增
	ws    map[string]*Worktree
	stats Stats
}

// NewCoordinator 以超级仓库的某个提交为起点创建协调器。
// 该提交附带的子模块表成为当前固定关系；悬空固定允许存在。
func NewCoordinator(store *Store, super RepoID, head CommitID) (*Coordinator, error) {
	if super == "" || head == "" {
		return nil, fmt.Errorf("%w: empty super repo or head", ErrInvalidParam)
	}
	r := store.Get(super)
	if r == nil {
		return nil, fmt.Errorf("%w: %q", ErrRepoNotFound, super)
	}
	if !r.HasCommit(head) {
		return nil, fmt.Errorf("%w: super head %q not in repo %q", ErrDanglingPin, head, super)
	}
	return &Coordinator{store: store, super: super, head: head, ws: map[string]*Worktree{}}, nil
}

// Generation 当前变更序号。
func (c *Coordinator) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen
}

// Head 超级仓库当前提交。
func (c *Coordinator) Head() CommitID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.head
}

// StatsSnapshot 返回解析开销计数快照。
func (c *Coordinator) StatsSnapshot() (recordsScanned, commitsVisited int64) {
	return c.stats.RecordsScanned.Load(), c.stats.CommitsVisited.Load()
}

// ResetStats 清零解析开销计数。
func (c *Coordinator) ResetStats() {
	c.stats.RecordsScanned.Store(0)
	c.stats.CommitsVisited.Store(0)
}

// SetCheckout 模拟工作区变化：设置挂载路径上的实际检出提交与脏标志。
// checkout 为空串表示移除该挂载路径上的检出（回到缺失）。
func (c *Coordinator) SetCheckout(path string, checkout CommitID, dirty bool) error {
	np, err := NormalizePath(path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if checkout == "" {
		delete(c.ws, np)
		return nil
	}
	c.ws[np] = &Worktree{Checkout: checkout, Dirty: dirty}
	return nil
}

// Checkout 读取挂载路径上的工作区状态。
func (c *Coordinator) Checkout(path string) (Worktree, bool) {
	np, err := NormalizePath(path)
	if err != nil {
		return Worktree{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	wt, ok := c.ws[np]
	if !ok {
		return Worktree{}, false
	}
	return *wt, true
}

func (c *Coordinator) headTable() Table {
	return c.store.Get(c.super).Commits[c.head].Table
}

// commitSuper 落地一次超级仓库子模块表变更：产生新提交并推进 head 与序号。
// 调用方必须持有写锁且已完成全部校验。
func (c *Coordinator) commitSuper(t Table) {
	c.seq++
	id := CommitID(fmt.Sprintf("%s#%d", c.super, c.seq))
	r := c.store.Get(c.super)
	r.AddCommit(&Commit{ID: id, Parents: []CommitID{c.head}, Table: t})
	c.head = id
	c.gen++
}

// classify 判定单个挂载点的互斥状态。
// 优先级：悬空固定 > 缺失 > 未提交修改 > 偏离 > 一致。
func (c *Coordinator) classify(n *mountNode) Status {
	if n.Repo == nil || n.Pinned == nil {
		return StatusDangling
	}
	wt, ok := c.ws[n.Path]
	if !ok {
		return StatusMissing
	}
	if wt.Dirty {
		return StatusDirty
	}
	if wt.Checkout != n.Record.Pinned {
		return StatusDiverged
	}
	return StatusClean
}

// Status 查询单个挂载点的状态。开销与挂载点总数无关。
func (c *Coordinator) Status(path string) (Status, error) {
	np, err := NormalizePath(path)
	if err != nil {
		return StatusMissing, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	node, err := c.resolveMount(np)
	if err != nil {
		return StatusMissing, err
	}
	return c.classify(node), nil
}

// Statuses 查询所有可解析挂载点的状态。循环挂载时报 ErrCycle。
func (c *Coordinator) Statuses() (map[string]Status, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	root, _, err := c.buildTree()
	if err != nil {
		return nil, err
	}
	out := map[string]Status{}
	walk(root, func(n *mountNode) { out[n.Path] = c.classify(n) })
	return out, nil
}

// Align 按固定更新：把每个挂载点的实际检出提交对齐到固定提交，
// 自顶向下递归到嵌套子模块。任一挂载点有未提交修改或悬空固定即
// 整体拒绝，一个挂载点都不动。
func (c *Coordinator) Align() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	root, dangling, err := c.buildTree()
	if err != nil {
		return err
	}
	if len(dangling) > 0 {
		return fmt.Errorf("%w: %q pins %q missing in repo %q",
			ErrDanglingPin, dangling[0].Path, dangling[0].Record.Pinned, dangling[0].Record.Repo)
	}
	var dirtyPath string
	walk(root, func(n *mountNode) {
		if dirtyPath == "" {
			if wt, ok := c.ws[n.Path]; ok && wt.Dirty {
				dirtyPath = n.Path
			}
		}
	})
	if dirtyPath != "" {
		return fmt.Errorf("%w: %q", ErrDirty, dirtyPath)
	}
	// 校验全部通过，应用不会失败：整体落地。
	walk(root, func(n *mountNode) {
		c.ws[n.Path] = &Worktree{Checkout: n.Record.Pinned}
	})
	c.gen++
	return nil
}

// Advance 按跟踪分支推进：对配置了跟踪分支的记录，把固定提交推进到
// 目标仓库中该分支的当前顶端，并产生一次超级仓库子模块表变更。
// 任何一条被拒，整批不变。
func (c *Coordinator) Advance() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	type plan struct {
		path string
		tip  CommitID
	}
	var tracked []SubmoduleRecord
	for _, rec := range c.headTable() {
		if rec.Tracking != "" {
			tracked = append(tracked, rec)
		}
	}
	if len(tracked) == 0 {
		return nil
	}
	// 按错误种类分阶段校验，保证只报适用次序中最靠前的一个。
	for _, rec := range tracked {
		if c.store.Get(rec.Repo) == nil {
			return fmt.Errorf("%w: %q", ErrRepoNotFound, rec.Repo)
		}
	}
	tips := make(map[string]CommitID, len(tracked))
	for _, rec := range tracked {
		tip, ok := c.store.Get(rec.Repo).Tip(rec.Tracking)
		if !ok {
			return fmt.Errorf("%w: %q in repo %q", ErrBranchNotFound, rec.Tracking, rec.Repo)
		}
		tips[rec.Path] = tip
	}
	for _, rec := range tracked {
		if !c.store.Get(rec.Repo).HasCommit(rec.Pinned) {
			return fmt.Errorf("%w: %q pins %q missing in repo %q", ErrDanglingPin, rec.Path, rec.Pinned, rec.Repo)
		}
	}
	for _, rec := range tracked {
		if wt, ok := c.ws[rec.Path]; ok && wt.Dirty {
			return fmt.Errorf("%w: %q", ErrDirty, rec.Path)
		}
	}
	var plans []plan
	for _, rec := range tracked {
		repo := c.store.Get(rec.Repo)
		tip := tips[rec.Path]
		if !repo.IsDescendantOrEqual(tip, rec.Pinned) {
			return fmt.Errorf("%w: %q tracking %q: %q is not a descendant of %q",
				ErrNonFastForward, rec.Path, rec.Tracking, tip, rec.Pinned)
		}
		plans = append(plans, plan{path: rec.Path, tip: tip})
	}
	// 校验全部通过，整批落地。
	next := make(Table, len(c.headTable()))
	for k, rec := range c.headTable() {
		next[k] = rec
	}
	for _, p := range plans {
		rec := next[p.path]
		rec.Pinned = p.tip
		next[p.path] = rec
	}
	c.commitSuper(next)
	return nil
}

// AddMount 在超级仓库当前子模块表上添加挂载记录。
// 路径冲突、目标仓库不存在、固定提交不存在为可区分错误。
func (c *Coordinator) AddMount(path string, repo RepoID, pin CommitID, tracking string) error {
	if repo == "" {
		return fmt.Errorf("%w: empty repo id", ErrInvalidParam)
	}
	np, err := NormalizePath(path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if repo == c.super {
		return fmt.Errorf("%w: repo %q mounted into itself at %q", ErrCycle, repo, np)
	}
	for _, rec := range c.headTable() {
		if pathsConflict(rec.Path, np) {
			return fmt.Errorf("%w: %q vs existing %q", ErrPathConflict, np, rec.Path)
		}
	}
	// 与可解析的嵌套挂载路径也要互不冲突（树不可解析时跳过，递归操作自会拒绝）。
	if root, _, terr := c.buildTree(); terr == nil {
		conflict := ""
		walk(root, func(n *mountNode) {
			if conflict == "" && pathsConflict(n.Path, np) {
				conflict = n.Path
			}
		})
		if conflict != "" {
			return fmt.Errorf("%w: %q vs nested %q", ErrPathConflict, np, conflict)
		}
	}
	r := c.store.Get(repo)
	if r == nil {
		return fmt.Errorf("%w: %q", ErrRepoNotFound, repo)
	}
	if tracking != "" {
		if _, ok := r.Tip(tracking); !ok {
			return fmt.Errorf("%w: %q in repo %q", ErrBranchNotFound, tracking, repo)
		}
	}
	if !r.HasCommit(pin) {
		return fmt.Errorf("%w: pin %q missing in repo %q", ErrDanglingPin, pin, repo)
	}
	// 新挂载的子树内不得把祖先链上的仓库再次挂入。
	if err := c.checkSubtreeCycles(r.Commits[pin], map[RepoID]bool{c.super: true, repo: true}); err != nil {
		return err
	}
	next := make(Table, len(c.headTable())+1)
	for k, rec := range c.headTable() {
		next[k] = rec
	}
	next[np] = SubmoduleRecord{Path: np, Repo: repo, Pinned: pin, Tracking: tracking}
	c.commitSuper(next)
	return nil
}

func (c *Coordinator) checkSubtreeCycles(commit *Commit, chain map[RepoID]bool) error {
	for _, rec := range commit.Table {
		if chain[rec.Repo] {
			return fmt.Errorf("%w: repo %q repeats on ancestor chain", ErrCycle, rec.Repo)
		}
		r := c.store.Get(rec.Repo)
		if r == nil {
			continue // 悬空记录在递归操作时拒绝，此处不阻断添加
		}
		sub, ok := r.Commits[rec.Pinned]
		if !ok {
			continue
		}
		chain[rec.Repo] = true
		err := c.checkSubtreeCycles(sub, chain)
		delete(chain, rec.Repo)
		if err != nil {
			return err
		}
	}
	return nil
}

// RemoveMount 移除超级仓库当前子模块表上的挂载记录。
// 挂载点（含其嵌套挂载）有未提交修改时拒绝，force 为真才允许丢弃。
func (c *Coordinator) RemoveMount(path string, force bool) error {
	np, err := NormalizePath(path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.headTable()[np]; !ok {
		return fmt.Errorf("%w: no mount at %q", ErrInvalidParam, np)
	}
	if !force {
		for p, wt := range c.ws {
			if wt.Dirty && (p == np || strings.HasPrefix(p, np+"/")) {
				return fmt.Errorf("%w: %q", ErrDirty, p)
			}
		}
	}
	next := make(Table, len(c.headTable())-1)
	for k, rec := range c.headTable() {
		if k != np {
			next[k] = rec
		}
	}
	c.commitSuper(next)
	for p := range c.ws {
		if p == np || strings.HasPrefix(p, np+"/") {
			delete(c.ws, p)
		}
	}
	return nil
}
