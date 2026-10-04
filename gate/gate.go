package gate

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/owners"
	"ontology/review"
)

// 错误哨兵：拒绝次序 参数非法 > PR 不存在 > 无权限 > 状态不符 > 自评。
var (
	ErrInvalid    = errors.New("invalid argument")
	ErrNotFound   = errors.New("pull request not found")
	ErrForbidden  = errors.New("permission denied")
	ErrState      = errors.New("state conflict")
	ErrSelfReview = errors.New("authors cannot review their own request")
)

// 合并拦截原因，按 Mergeable 报告次序定义。
var (
	ErrMerged        = errors.New("pull request already merged")
	ErrDraft         = errors.New("pull request is a draft")
	ErrChangeRequest = errors.New("a valid request for changes exists")
	ErrApprovals     = errors.New("not enough approvals")
	ErrOwnerApproval = errors.New("missing required owner approval")
	ErrCheckFailure  = errors.New("required check failed")
	ErrCheckPending  = errors.New("required check not completed")
	ErrStaleBase     = errors.New("pull request base is stale")
)

// Config 是闸门配置。
type Config struct {
	N             int
	DismissStale  bool
	RequireOwners bool
	Strict        bool
	Required      []string
}

// Gate 是合并准入闸门。
type Gate struct {
	mu     sync.RWMutex
	cfg    Config
	rules  *owners.Rules
	store  *review.Store
	prs    map[int]*pr
	nextID int
	t      int
}

type pr struct {
	id     int
	author string
	files  []string // 替换式文件集合；判定需要有序遍历（取字节序最小）
	draft  bool
	merged bool
	base   int
	head   int
}

// New 创建闸门，配置与规则均做防御性拷贝。
func New(cfg Config, rules []owners.Rule) *Gate {
	return &Gate{
		cfg:   Config{N: cfg.N, DismissStale: cfg.DismissStale, RequireOwners: cfg.RequireOwners, Strict: cfg.Strict, Required: append([]string(nil), cfg.Required...)},
		rules: owners.New(rules),
		store: review.NewStore(),
		prs:   make(map[int]*pr),
	}
}

// SetPerm 设置用户权限等级。
func (g *Gate) SetPerm(user string, level review.Level) error {
	if user == "" || level < review.None || level > review.Admin {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.store.SetLevel(user, level)
	return nil
}

// Open 打开新 PR，base 为当前 T，head 为 1。
func (g *Gate) Open(author string, files []string, draft bool) (int, error) {
	if author == "" || !validFiles(files) {
		return 0, ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	id := g.nextID
	g.prs[id] = &pr{
		id:     id,
		author: author,
		files:  append([]string(nil), files...),
		draft:  draft,
		base:   g.t,
		head:   1,
	}
	return id, nil
}

// Push 追加一个 head 并替换文件集合。
func (g *Gate) Push(id int, files []string) error {
	if !validFiles(files) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	if p.merged {
		return ErrState
	}
	p.head++
	p.files = append([]string(nil), files...)
	if g.cfg.DismissStale {
		g.store.ClearApprovals(id)
	}
	return nil
}

// Ready 去除草稿标记。
func (g *Gate) Ready(id int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	if p.merged || !p.draft {
		return ErrState
	}
	p.draft = false
	return nil
}

// UpdateBranch 同步基线到 T，head 加 1，文件与裁决不变。
func (g *Gate) UpdateBranch(id int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	if p.merged || p.base == g.t {
		return ErrState
	}
	p.base = g.t
	p.head++
	return nil
}

// Review 提交评审裁决。
func (g *Gate) Review(id int, user string, v review.Verdict) error {
	if user == "" || !validVerdict(v) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	if p.merged {
		return ErrState
	}
	if v != review.Comment && user == p.author {
		return ErrSelfReview
	}
	if v == review.Comment {
		return nil // 任何人可评论，不改变先前裁决
	}
	g.store.SetVerdict(id, user, v)
	return nil
}

// Dismiss 由 admin 清除某评审人的裁决。
func (g *Gate) Dismiss(id int, reviewer, actor string) error {
	if reviewer == "" || actor == "" {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	if g.store.Level(actor) != review.Admin {
		return ErrForbidden
	}
	if p.merged {
		return ErrState
	}
	if !g.store.ClearVerdict(id, reviewer) {
		return ErrState
	}
	return nil
}

// Report 上报检查结果。
func (g *Gate) Report(id int, check string, head int, st review.Status) error {
	if check == "" || head < 1 || !validStatus(st) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	if p.merged || head > p.head {
		return ErrState
	}
	g.store.SetCheck(id, check, head, st)
	return nil
}

// Mergeable 返回首个拦截原因，无拦截返回 nil。
func (g *Gate) Mergeable(id int) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	return g.blockedLocked(p)
}

// Merge 合并 PR 并推进 T。
func (g *Gate) Merge(id int, actor string) error {
	if actor == "" {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[id]
	if !ok {
		return ErrNotFound
	}
	level := g.store.Level(actor)
	if level != review.Write && level != review.Admin {
		return ErrForbidden
	}
	if err := g.blockedLocked(p); err != nil {
		return err
	}
	p.merged = true
	g.t++
	return nil
}

// blockedLocked 按固定次序返回首个拦截原因；调用方须持锁。
func (g *Gate) blockedLocked(p *pr) error {
	id := p.id
	switch {
	case p.merged:
		return ErrMerged
	case p.draft:
		return ErrDraft
	}
	approvals := g.store.ActiveVerdicts(id, review.Approve)
	changeReqs := g.store.ActiveVerdicts(id, review.RequestChanges)
	if len(changeReqs) > 0 {
		return ErrChangeRequest
	}
	if len(approvals) < g.cfg.N {
		return ErrApprovals
	}
	if g.cfg.RequireOwners {
		if path := g.missingOwnerApproval(p, approvals); path != "" {
			return fmt.Errorf("%w: %s", ErrOwnerApproval, path)
		}
	}
	for _, check := range g.cfg.Required {
		st, ok := g.store.Check(id, check, p.head)
		if ok && st == review.Failure {
			return fmt.Errorf("%w: %s", ErrCheckFailure, check)
		}
	}
	for _, check := range g.cfg.Required {
		st, ok := g.store.Check(id, check, p.head)
		if !ok {
			return fmt.Errorf("%w: %s", ErrCheckPending, check)
		}
		if st == review.Failure || st == review.Pending {
			return fmt.Errorf("%w: %s", ErrCheckPending, check)
		}
	}
	if g.cfg.Strict && p.base < g.t {
		return ErrStaleBase
	}
	return nil
}

// missingOwnerApproval 返回缺少有效属主批准的字节序最小文件；无缺失返回 ""。
func (g *Gate) missingOwnerApproval(p *pr, approvals map[string]struct{}) string {
	var missing []string
	for _, file := range p.files {
		ownersOf := g.rules.Owners(file)
		eligible := make([]string, 0, len(ownersOf))
		for _, owner := range ownersOf {
			if owner != p.author {
				eligible = append(eligible, owner)
			}
		}
		if len(eligible) == 0 {
			continue // 去掉作者后无剩余属主：免除
		}
		covered := false
		for _, owner := range eligible {
			if _, ok := approvals[owner]; ok {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, file)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return missing[0]
}

func validFiles(files []string) bool {
	if len(files) < 1 || len(files) > 1000 {
		return false
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if file == "" {
			return false
		}
		if _, dup := seen[file]; dup {
			return false
		}
		seen[file] = struct{}{}
	}
	return true
}

func validVerdict(v review.Verdict) bool {
	return v >= review.Approve && v <= review.Comment
}

func validStatus(s review.Status) bool {
	return s >= review.Pending && s <= review.Skipped
}
