package submod

import (
	"fmt"
	"strings"
	"sync"
)

// Status 是挂载点的工作区状态，五种含义互斥。
// 判定优先级：悬空固定 > 有未提交修改 > 检出偏离 > 缺失 > 一致。
type Status int

const (
	StatusConsistent Status = iota // 一致：检出提交等于固定提交
	StatusDiverged                 // 检出偏离：检出提交与固定提交不同
	StatusDirty                    // 有未提交修改（优先于检出偏离）
	StatusDangling                 // 悬空固定：固定提交不存在于目标仓库
	StatusMissing                  // 缺失：挂载路径上没有检出
)

var statusNames = map[Status]string{
	StatusConsistent: "一致",
	StatusDiverged:   "检出偏离",
	StatusDirty:      "未提交修改",
	StatusDangling:   "悬空固定",
	StatusMissing:    "缺失",
}

func (s Status) String() string { return statusNames[s] }

// checkout 是一个挂载点的工作区检出状态。
type checkout struct {
	commit string
	dirty  bool
}

// Coordinator 是子模块固定与更新协调器。
//
// 并发：所有公开方法在内部互斥锁下串行执行，因此任意并发调用的结果
// 都等价于某个串行顺序；每次成功变更把 revision 加一，revision 的
// 相对顺序即该串行顺序。被拒绝的调用不改变任何固定点、检出状态或序号。
type Coordinator struct {
	mu          sync.Mutex
	repos       map[string]*Repo
	superID     string
	superBranch string
	checkouts   map[string]*checkout // 挂载树路径 -> 检出状态
	revision    uint64
	visits      uint64 // 记录解析步数（性能可验证性埋点，仅供测试读取）
}

// NewCoordinator 载入仓库集合与超级仓库分支。
// 载入时校验所有提交的子模块表（非法表报错），但允许悬空固定存在。
func NewCoordinator(repos map[string]*Repo, superID, superBranch string) (*Coordinator, error) {
	super, ok := repos[superID]
	if !ok {
		return nil, fmt.Errorf("submod: 超级仓库 %q 不存在", superID)
	}
	if _, ok := super.Branches[superBranch]; !ok {
		return nil, fmt.Errorf("submod: 超级仓库 %q 的分支 %q 不存在", superID, superBranch)
	}
	for _, repo := range repos {
		for _, com := range repo.Commits {
			norm, err := validateTable(com.Table)
			if err != nil {
				return nil, err
			}
			com.Table = norm
		}
	}
	return &Coordinator{
		repos:       repos,
		superID:     superID,
		superBranch: superBranch,
		checkouts:   map[string]*checkout{},
	}, nil
}

// Revision 返回当前序号。每次成功变更（含工作区事件）序号加一。
func (c *Coordinator) Revision() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.revision
}

// Visits 返回累计记录解析步数（性能证明用，测试专用）。
func (c *Coordinator) Visits() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.visits
}

// ResetVisits 清零记录解析步数（性能证明用，测试专用）。
func (c *Coordinator) ResetVisits() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.visits = 0
}

// currentTable 返回超级仓库当前分支顶端提交附带的子模块表。
// 调用方须持有锁。
func (c *Coordinator) currentTable() Table {
	super := c.repos[c.superID]
	tip := super.Branches[c.superBranch]
	return super.Commits[tip].Table
}

// nestedTable 解析一条记录的固定提交所附带的子模块表；
// 目标仓库或固定提交不存在（悬空固定）时返回 nil。
// 调用方须持有锁。
func (c *Coordinator) nestedTable(rec Record) Table {
	repo := c.repos[rec.Repo]
	if repo == nil {
		return nil
	}
	com := repo.Commits[rec.Commit]
	if com == nil {
		return nil
	}
	return com.Table
}

// walk 自顶向下遍历整棵挂载树，对每个挂载点调用 fn。
// 沿每条根到叶挂载链检测循环挂载（某仓库在自己的祖先链上再次出现），
// 发现即返回 ErrCodeCircularMount；悬空固定的记录不再向下递归。
// 调用方须持有锁。
func (c *Coordinator) walk(fn func(treePath string, rec Record)) error {
	return c.walkTable(c.currentTable(), "", []string{c.superID}, fn)
}

// walkTable 是 walk 的递归实现；chain 是从超级仓库到当前表的仓库链。
func (c *Coordinator) walkTable(t Table, prefix string, chain []string, fn func(string, Record)) error {
	for _, p := range sortedKeys(t) {
		rec := t[p]
		c.visits++
		for _, id := range chain {
			if id == rec.Repo {
				return errf(ErrCodeCircularMount, joinPath(prefix, p),
					"仓库 %q 在自己的祖先挂载链上再次出现", rec.Repo)
			}
		}
		treePath := joinPath(prefix, p)
		fn(treePath, rec)
		if sub := c.nestedTable(rec); sub != nil {
			if err := c.walkTable(sub, treePath, append(chain, rec.Repo), fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolve 沿挂载树解析一个挂载树路径，返回该路径的记录。
// 只访问路径所在挂载链上的记录，与树中无关分支的规模无关。
// 调用方须持有锁。
func (c *Coordinator) resolve(path string) (Record, error) {
	chain := []string{c.superID}
	table := c.currentTable()
	remaining := path
	for {
		key, rec, ok := findRecord(table, remaining)
		if !ok {
			return Record{}, errf(ErrCodeMountNotFound, path, "挂载点不存在: %s", path)
		}
		c.visits++
		for _, id := range chain {
			if id == rec.Repo {
				return Record{}, errf(ErrCodeCircularMount, path,
					"仓库 %q 在自己的祖先挂载链上再次出现", rec.Repo)
			}
		}
		if key == remaining {
			return rec, nil
		}
		sub := c.nestedTable(rec)
		if sub == nil {
			return Record{}, errf(ErrCodeMountNotFound, path, "挂载点不存在（祖先固定悬空）: %s", path)
		}
		chain = append(chain, rec.Repo)
		table = sub
		remaining = remaining[len(key)+1:]
	}
}

// findRecord 在表中查找键为 remaining 或其某个段对齐前缀的记录。
// 由于表内路径互不为祖先后代，至多命中一条；逐前缀 map 查找，不扫描全表。
func findRecord(t Table, remaining string) (string, Record, bool) {
	segs := strings.Split(remaining, "/")
	for i := 1; i <= len(segs); i++ {
		k := strings.Join(segs[:i], "/")
		if rec, ok := t[k]; ok {
			return k, rec, true
		}
	}
	return "", Record{}, false
}

// statusOf 判定单个挂载点的状态，开销与挂载点总数无关。
// 调用方须持有锁。
func (c *Coordinator) statusOf(rec Record, co *checkout) Status {
	repo := c.repos[rec.Repo]
	if repo == nil || !repo.hasCommit(rec.Commit) {
		return StatusDangling
	}
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

// StatusAll 返回整棵挂载树每个挂载点的状态。循环挂载时整体拒绝。
func (c *Coordinator) StatusAll() (map[string]Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]Status{}
	err := c.walk(func(p string, rec Record) {
		out[p] = c.statusOf(rec, c.checkouts[p])
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StatusAt 返回单个挂载点的状态，开销不随挂载点总数增长。
func (c *Coordinator) StatusAt(path string) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	npath, err := NormalizePath(path)
	if err != nil {
		return StatusConsistent, err
	}
	rec, err := c.resolve(npath)
	if err != nil {
		return StatusConsistent, err
	}
	return c.statusOf(rec, c.checkouts[npath]), nil
}

// SuperTable 返回超级仓库当前子模块表的副本（检视用）。
func (c *Coordinator) SuperTable() map[string]Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Record, len(c.currentTable()))
	for k, v := range c.currentTable() {
		out[k] = v
	}
	return out
}

// CheckoutState 返回一个挂载树路径上的检出状态（检视/测试用）。
func (c *Coordinator) CheckoutState(path string) (commit string, dirty bool, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if co := c.checkouts[path]; co != nil {
		return co.commit, co.dirty, true
	}
	return "", false, false
}

// SetCheckout 模拟工作区事件：某挂载点检出到指定提交（保留脏标记）。
func (c *Coordinator) SetCheckout(path, commit string) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	npath, err := NormalizePath(path)
	if err != nil {
		return c.revision, err
	}
	co := c.checkouts[npath]
	if co == nil {
		co = &checkout{}
		c.checkouts[npath] = co
	}
	co.commit = commit
	c.revision++
	return c.revision, nil
}

// SetDirty 模拟工作区事件：设置某挂载点的未提交修改标记。
func (c *Coordinator) SetDirty(path string, dirty bool) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	npath, err := NormalizePath(path)
	if err != nil {
		return c.revision, err
	}
	co := c.checkouts[npath]
	if co == nil {
		return c.revision, errf(ErrCodeMountNotFound, npath, "无检出，无法标记未提交修改: %s", npath)
	}
	co.dirty = dirty
	c.revision++
	return c.revision, nil
}

// ClearCheckout 模拟工作区事件：移除某挂载点的检出（回到缺失）。
func (c *Coordinator) ClearCheckout(path string) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	npath, err := NormalizePath(path)
	if err != nil {
		return c.revision, err
	}
	delete(c.checkouts, npath)
	c.revision++
	return c.revision, nil
}
