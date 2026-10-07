package pathlock

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Lock 表示一把路径锁。锁标识全局唯一且永不复用。
type Lock struct {
	ID        uint64
	Path      string // 规范化后的路径
	Owner     string
	CreatedAt time.Time
}

// AuditEntry 记录一次管理员强制释放。普通释放不记入审计。
type AuditEntry struct {
	LockID uint64
	Path   string
	Owner  string // 被释放锁的原持有者
	Admin  string // 发起强制释放的管理员
	Time   time.Time
}

// PathConflict 是推送校验发现的一处冲突：被他人持有的路径及其持有者。
type PathConflict struct {
	Path   string
	Holder string
}

// PushResult 是推送校验的裁决结果。
type PushResult struct {
	Accepted  bool           // 整批是否放行
	Conflicts []PathConflict // 拒绝时的全部冲突，按路径字节序排列且不重复
	Released  []uint64       // 「校验并顺带释放」放行时被释放的锁标识
}

// Service 是路径锁服务。所有方法可任意并发调用，
// 观察到的结果都等价于某个串行顺序（内部由单把互斥锁串行化）。
type Service struct {
	mu     sync.Mutex
	admins map[string]bool
	byID   map[uint64]*Lock
	trie   *pathTrie
	index  *pathIndex
	nextID uint64
	audit  []AuditEntry
	now    func() time.Time
}

// NewService 创建一个空的路径锁服务，admins 为具备强制释放权限的用户。
func NewService(admins ...string) *Service {
	s := &Service{
		admins: make(map[string]bool, len(admins)),
		byID:   make(map[uint64]*Lock),
		trie:   newPathTrie(),
		index:  newPathIndex(),
		now:    time.Now,
	}
	for _, a := range admins {
		s.admins[a] = true
	}
	return s
}

// Lock 尝试为 user 锁定 path。成功返回新锁；
// 失败返回 *Error，且不改变任何锁、审计记录与序号。
func (s *Service) Lock(user, path string) (Lock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" {
		return Lock{}, invalidArg("空用户")
	}
	np, err := NormalizePath(path)
	if err != nil {
		return Lock{}, err
	}
	if l := s.trie.findExact(np); l != nil {
		if l.Owner == user {
			return Lock{}, &Error{Code: ErrHeldBySelf, Message: "已由本人持有", Holder: user}
		}
		return Lock{}, &Error{Code: ErrHeldByOther, Message: "已被他人持有", Holder: l.Owner}
	}
	if c := s.trie.findConflict(np, user); c != nil {
		return Lock{}, &Error{
			Code:     ErrAncestorOrDescendantConflict,
			Message:  "祖先或后代已被他人持有",
			Holder:   c.Owner,
			Conflict: &Lock{ID: c.ID, Path: c.Path, Owner: c.Owner, CreatedAt: c.CreatedAt},
		}
	}
	// 全部校验通过后才消耗序号与时间戳，保证被拒绝的调用零副作用。
	s.nextID++
	l := &Lock{ID: s.nextID, Path: np, Owner: user, CreatedAt: s.now()}
	s.byID[l.ID] = l
	s.trie.add(np, l)
	s.index.insert(np, l)
	return *l, nil
}

// Unlock 释放标识为 id 的锁。force 为 true 时是管理员强制释放，
// 须由管理员发起并记入审计；普通释放不记审计。
// 释放成功后同一锁标识永远不可再被释放第二次。
func (s *Service) Unlock(user string, id uint64, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" {
		return invalidArg("空用户")
	}
	if force && !s.admins[user] {
		return &Error{Code: ErrPermissionDenied, Message: "无权强制释放"}
	}
	l := s.byID[id]
	if l == nil {
		return &Error{Code: ErrLockNotFound, Message: "锁不存在"}
	}
	if !force && l.Owner != user {
		return &Error{Code: ErrNotOwner, Message: "非持有者", Holder: l.Owner}
	}
	s.removeLock(l)
	if force {
		s.audit = append(s.audit, AuditEntry{
			LockID: l.ID,
			Path:   l.Path,
			Owner:  l.Owner,
			Admin:  user,
			Time:   s.now(),
		})
	}
	return nil
}

// ValidatePush 对一次推送做全有或全无的持锁核验：
// 批内任一路径（含其祖先或后代）被他人持锁则整批拒绝，
// Conflicts 列出全部冲突路径及持有者，按路径字节序排列且不重复。
// releaseOnPass 为 true 时，仅当整批放行才释放批内由本人持有的全部锁，
// 并在 Released 中列出被释放的锁标识；拒绝时一把都不释放。
// 放行与拒绝除上述顺带释放外都不改变任何锁的状态。
func (s *Service) ValidatePush(user string, paths []string, releaseOnPass bool) (PushResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" {
		return PushResult{}, invalidArg("空用户")
	}
	if len(paths) == 0 {
		return PushResult{}, invalidArg("空批次")
	}
	var norm []string
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		np, err := NormalizePath(p)
		if err != nil {
			return PushResult{}, err
		}
		if !seen[np] {
			seen[np] = true
			norm = append(norm, np)
		}
	}
	conflicts := make(map[string]*Lock)
	for _, np := range norm {
		s.trie.collectConflicts(np, user, conflicts)
	}
	if len(conflicts) > 0 {
		list := make([]PathConflict, 0, len(conflicts))
		for p, l := range conflicts {
			list = append(list, PathConflict{Path: p, Holder: l.Owner})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
		return PushResult{Accepted: false, Conflicts: list}, nil
	}
	res := PushResult{Accepted: true}
	if releaseOnPass {
		for _, np := range norm {
			if l := s.trie.findExact(np); l != nil && l.Owner == user {
				res.Released = append(res.Released, l.ID)
				s.removeLock(l)
			}
		}
	}
	return res, nil
}

// ListByPrefix 按路径前缀分页列出锁，结果按路径字节序排列。
// prefix 为目录前缀：匹配等于 prefix 或以其为祖先目录的路径，空前缀匹配全部。
// cursor 为上一页最后一条的路径（首页传 ""），返回 path > cursor 的下一页；
// nextCursor 供继续翻页，hasMore 为 false 表示已到末尾。
// 翻页期间新建或释放的锁，要么整把出现在某一页，要么完全不出现。
func (s *Service) ListByPrefix(prefix, cursor string, limit int) (locks []Lock, nextCursor string, hasMore bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return nil, "", false, invalidArg("页大小须为正数")
	}
	np := ""
	if prefix != "" {
		if np, err = NormalizePath(prefix); err != nil {
			return nil, "", false, err
		}
	}
	if cursor != "" {
		if _, err = NormalizePath(cursor); err != nil {
			return nil, "", false, err
		}
	}
	// 匹配项都落在字节区间 [np, np+"0") 内（'/'=0x2F，'0'=0x30），
	// 越过区间上界即可提前停止。
	upper := np + "0"
	s.index.ascend(cursor, func(n *indexNode) bool {
		p := n.path
		if np != "" && p != np && !strings.HasPrefix(p, np+"/") {
			return p < upper // 区间内的非匹配项跳过，越界则停止
		}
		if len(locks) == limit {
			hasMore = true
			return false
		}
		locks = append(locks, *n.lock)
		return true
	})
	if len(locks) > 0 {
		nextCursor = locks[len(locks)-1].Path
	}
	return locks, nextCursor, hasMore, nil
}

// Lookup 按路径查询当前持有的锁。
func (s *Service) Lookup(path string) (Lock, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	np, err := NormalizePath(path)
	if err != nil {
		return Lock{}, false, err
	}
	if l := s.trie.findExact(np); l != nil {
		return *l, true, nil
	}
	return Lock{}, false, nil
}

// AuditLog 返回强制释放审计记录的副本，按发生顺序排列。
func (s *Service) AuditLog() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditEntry(nil), s.audit...)
}

func (s *Service) removeLock(l *Lock) {
	delete(s.byID, l.ID)
	s.trie.remove(l.Path)
	s.index.remove(l.Path)
}

// statsForTest 返回 (trie 节点访问数, 索引关键字比较数) 的累计值，
// 供测试以确定性方式验证操作开销与全库锁总数无关。
func (s *Service) statsForTest() (trieVisits, indexCompares int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trie.visits, s.index.compares
}

func (s *Service) resetStatsForTest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trie.visits = 0
	s.index.compares = 0
}
