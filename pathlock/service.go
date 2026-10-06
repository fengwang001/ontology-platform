package pathlock

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Lock 描述一把路径锁。
type Lock struct {
	ID        string
	Path      string
	Owner     string
	CreatedAt time.Time
}

// AuditEntry 是管理员强制释放的审计记录。Seq 是全局单调序号，
// 仅在真正成功的强制释放时递增；被拒绝的调用不消耗序号。
type AuditEntry struct {
	Seq    int64
	LockID string
	Path   string
	Owner  string
	Admin  string
	Time   time.Time
}

// Conflict 是推送校验返回的单条冲突（批次路径字节序排列、去重）。
type Conflict struct {
	Path  string
	Owner string
}

// VerifyResult 是推送校验的裁决结果。
type VerifyResult struct {
	Allowed     bool
	Conflicts   []Conflict
	ReleasedIDs []string
}

// ListPage 是前缀查询的一页结果。翻页必须携带 Snapshot 与 NextToken，
// 二者共同锁定某一不可变版本，保证整把记录出现、不重复、不遗漏。
type ListPage struct {
	Locks     []Lock
	NextToken string
	Snapshot  string
	HasMore   bool
}

// defaultPageSize 是未指定 limit 时的单页上限。
const defaultPageSize = 100

// Service 是仓库级路径锁服务。所有公开方法经同一把互斥锁串行化，
// 因而任意并发观察都等价于某个串行顺序；持锁期间只做内存有序树操作。
type Service struct {
	mu      sync.Mutex
	root    *treapNode
	pathOf  map[string]string // Lock.ID -> 当前 Path（精确存在的锁）
	seq     int64
	snapSeq uint64
	now     func() time.Time
	snaps   map[string]*treapNode
	audits  []AuditEntry
}

// NewService 创建空服务。
func NewService() *Service {
	return &Service{
		pathOf: make(map[string]string),
		now:    time.Now,
		snaps:  make(map[string]*treapNode),
	}
}

// newID 生成全局唯一且永不复用的锁标识：单调序号 + 128 位随机后缀。
func (s *Service) newID() string {
	s.seq++
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败不可恢复；叠加时间字节也绝不与旧序号碰撞。
		copy(b[:], s.now().AppendFormat(nil, time.RFC3339Nano))
	}
	return fmt.Sprintf("lock-%d-%s", s.seq, hex.EncodeToString(b[:]))
}

// ancestors 返回 path 的全部祖先路径（由近及远，不含 path 自身）。
func ancestors(path string) []string {
	out := make([]string, 0)
	for {
		i := lastSlash(path)
		if i < 0 {
			return out
		}
		path = path[:i]
		out = append(out, path)
	}
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// pickConflict 选出裁决所附冲突锁：路径最短，长度相同取路径字节序最小。
func pickConflict(best Lock, have bool, c Lock) (Lock, bool) {
	if !have || len(c.Path) < len(best.Path) ||
		(len(c.Path) == len(best.Path) && c.Path < best.Path) {
		return c, true
	}
	return best, have
}

// Acquire 对规范化后的路径加锁。同一用户可越过自己的祖先/后代排他，
// 他人冲突按「精确路径 > 祖先/后代」的错误次序裁决，且不改动任何现有锁。
func (s *Service) Acquire(user, path string) (Lock, error) {
	np, err := Normalize(path)
	if err != nil {
		return Lock{}, ErrInvalidPath
	}
	if user == "" {
		return Lock{}, ErrEmptyUser
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if l, ok := treapGet(s.root, np); ok {
		if l.Owner == user {
			return Lock{}, errSelfHeld(l)
		}
		return Lock{}, errOtherHeld(l)
	}

	onlySelf := true
	var conflict Lock
	var have bool
	note := func(l Lock) {
		if l.Owner != user {
			conflict, have = pickConflict(conflict, have, l)
			onlySelf = false
		}
	}
	for _, a := range ancestors(np) {
		if l, ok := treapGet(s.root, a); ok {
			note(l)
		}
	}
	treapAscend(s.root, np+"/", func(l Lock) bool {
		if !underPrefix(l.Path, np) {
			return false
		}
		note(l)
		return true
	})
	if !onlySelf {
		return Lock{}, errAncestor(conflict)
	}

	l := Lock{ID: s.newID(), Path: np, Owner: user, CreatedAt: s.now()}
	s.root = treapInsert(s.root, l)
	s.pathOf[l.ID] = np
	return l, nil
}

// Release 释放锁。force=true 时要求 admin 具备管理员权限；普通释放只有
// 持有者本人可执行。成功的普通释放不写审计；强制释放追加审计并推进序号。
func (s *Service) Release(admin, lockID string, force bool) (Lock, error) {
	if admin == "" {
		return Lock{}, ErrEmptyUser
	}
	if force && !IsAdmin(admin) {
		return Lock{}, errNotAdmin()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path, ok := s.pathOf[lockID]
	if !ok {
		return Lock{}, errLockNotFound()
	}
	l, ok := treapGet(s.root, path)
	if !ok || l.ID != lockID {
		return Lock{}, errLockNotFound()
	}
	if !force && l.Owner != admin {
		return Lock{}, errNotOwner(l)
	}

	s.root = treapDelete(s.root, path)
	delete(s.pathOf, lockID)
	if force {
		s.seq++
		s.audits = append(s.audits, AuditEntry{
			Seq:    s.seq,
			LockID: l.ID,
			Path:   l.Path,
			Owner:  l.Owner,
			Admin:  admin,
			Time:   s.now(),
		})
	}
	return l, nil
}

// Verify 对一批路径做全有或全无的持锁核验。release=true 时仅在整批
// 放行后，顺带释放批次中由本人持有的锁；拒绝时一把都不释放。
func (s *Service) Verify(user string, paths []string, release bool) (VerifyResult, error) {
	if user == "" {
		return VerifyResult{}, ErrEmptyUser
	}
	if len(paths) == 0 {
		return VerifyResult{}, ErrEmptyBatch
	}
	norm := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		np, err := Normalize(p)
		if err != nil {
			return VerifyResult{}, ErrInvalidPath
		}
		if _, dup := seen[np]; dup {
			continue
		}
		seen[np] = struct{}{}
		norm = append(norm, np)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	conflicts := make(map[string]string)
	for _, np := range norm {
		checkOne(s.root, np, func(lockPath, owner string) {
			if owner != user {
				conflicts[lockPath] = owner
			}
		})
	}
	if len(conflicts) > 0 {
		return VerifyResult{Allowed: false, Conflicts: sortedConflicts(conflicts)}, nil
	}

	res := VerifyResult{Allowed: true, Conflicts: []Conflict{}}
	if release {
		for _, np := range norm {
			if l, ok := treapGet(s.root, np); ok && l.Owner == user {
				s.root = treapDelete(s.root, np)
				delete(s.pathOf, l.ID)
				res.ReleasedIDs = append(res.ReleasedIDs, l.ID)
			}
		}
	}
	return res, nil
}

// checkOne 枚举与 np 冲突的全部现存锁（精确、祖先、后代），
// 以 (锁路径, 持有者) 回调。
func checkOne(root *treapNode, np string, fn func(path, owner string)) {
	if l, ok := treapGet(root, np); ok {
		fn(l.Path, l.Owner)
	}
	for _, a := range ancestors(np) {
		if l, ok := treapGet(root, a); ok {
			fn(l.Path, l.Owner)
		}
	}
	treapAscend(root, np+"/", func(l Lock) bool {
		if !underPrefix(l.Path, np) {
			return false
		}
		fn(l.Path, l.Owner)
		return true
	})
}

// sortedConflicts 按路径字节序返回冲突列表（键集通常很小，用插入排序）。
func sortedConflicts(m map[string]string) []Conflict {
	out := make([]Conflict, 0, len(m))
	for p, owner := range m {
		out = append(out, Conflict{Path: p, Owner: owner})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Path > out[j].Path; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// List 按路径前缀列出锁。prefix 为空表示全仓库；否则须为合法规范化路径。
// 首次调用后用返回的 Snapshot 与 NextToken 续页，以固定一个不可变版本：
// 翻页期间的新建/释放，要么整把落在某一页，要么完全不出现。
func (s *Service) List(prefix, afterToken, snapshot string, limit int) (ListPage, error) {
	var np string
	if prefix != "" {
		var err error
		np, err = Normalize(prefix)
		if err != nil {
			return ListPage{}, ErrInvalidPath
		}
	}
	if limit <= 0 {
		limit = defaultPageSize
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	root := s.root
	token := snapshot
	if snapshot == "" {
		token = s.pinSnapshot(root)
	} else {
		r, ok := s.snaps[snapshot]
		if !ok {
			return ListPage{}, errLockNotFound()
		}
		root = r
	}

	start := np
	if afterToken != "" {
		cur, ok := decodeCursor(afterToken)
		if !ok {
			return ListPage{}, ErrInvalidPath
		}
		start = cur
	}

	page := ListPage{Snapshot: token, Locks: []Lock{}}
	var firstAfter string
	treapAscend(root, start, func(l Lock) bool {
		if np != "" && !underPrefix(l.Path, np) {
			return false
		}
		if len(page.Locks) == limit {
			firstAfter = l.Path
			return false
		}
		page.Locks = append(page.Locks, l)
		return true
	})
	if firstAfter != "" {
		page.HasMore = true
		page.NextToken = encodeCursor(firstAfter)
	}
	return page, nil
}

func encodeCursor(path string) string { return "c:" + path }

func decodeCursor(tok string) (string, bool) {
	if len(tok) > 2 && tok[:2] == "c:" {
		return tok[2:], true
	}
	return "", false
}

// ReleaseSnapshot 释放分页快照占用；未登记的 token 视为已释放，静默成功。
//
// pinSnapshot 把某一树根登记为命名快照并返回令牌；令牌单调生成、永不复用。
func (s *Service) pinSnapshot(root *treapNode) string {
	s.snapSeq++
	token := fmt.Sprintf("snap-%d", s.snapSeq)
	s.snaps[token] = root
	return token
}

func (s *Service) ReleaseSnapshot(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snaps, token)
}

// AuditLog 返回强制释放审计的副本，按序号升序。
func (s *Service) AuditLog() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.audits))
	copy(out, s.audits)
	return out
}
