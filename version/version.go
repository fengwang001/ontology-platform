// Package version 实现带版本链、删除标记与对象锁的多租户对象存储桶。
package version

import (
	"sync"

	"ontology/authz"
	"ontology/lock"
)

// 参数上界。
const (
	maxNow   = uint64(1_000_000_000_000)
	maxSize  = uint64(1_000_000_000_000)
	maxUntil = uint64(1_000_000_000_000)
	maxDef   = uint64(1_000_000_000)
	maxBatch = 1000
)

// ErrKind 区分拒绝原因与 Get 的两类"无数据"。
type ErrKind int

const (
	ErrInvalidParam ErrKind = iota
	ErrClockRollback
	ErrNoPermission
	ErrVersionNotFound
	ErrLegalHold
	ErrComplianceRetention
	ErrGovernanceRetention
	ErrNotExist
	ErrMarkedDeleted
)

// Error 是一次被拒绝操作的结果；Index 仅对 DeleteVersions 有意义。
type Error struct {
	Kind  ErrKind
	Index int
}

func (e *Error) Error() string { return "version: " + e.Kind.String() }

func errOf(kind ErrKind) *Error { return &Error{Kind: kind, Index: -1} }

// reasonKind 把 lock 的判定结论映射为错误种类。
func reasonKind(r lock.Reason) ErrKind {
	switch r {
	case lock.ByLegalHold:
		return ErrLegalHold
	case lock.ByCompliance:
		return ErrComplianceRetention
	default:
		return ErrGovernanceRetention
	}
}

// String 返回错误种类的可读名。
func (k ErrKind) String() string {
	return [...]string{
		"invalid param", "clock rollback", "no permission", "version not found",
		"legal hold", "compliance retention", "governance retention",
		"not exist", "marked deleted",
	}[k]
}

// AuditEntry 记录一次经绕过才成功的 GOVERNANCE 删除。
type AuditEntry struct {
	Key            string
	Ver            uint64
	Now            uint64
	OldRetainUntil uint64
}

// Info 是 Get 返回的当前数据版本视图。
type Info struct {
	Key         string
	Ver         uint64
	Size        uint64
	Mode        lock.Mode
	RetainUntil uint64
	Hold        bool
}

// Item 是 DeleteVersions 的一个待删项。
type Item struct {
	Key string
	Ver uint64
}

// record 是一个版本记录（数据版本或删除标记）。
type record struct {
	ver    uint64
	size   uint64
	marker bool
	mode   lock.Mode
	until  uint64
	hold   bool
	prev   *record
	next   *record
}

// chain 是一个键的版本链：current 为现存版本中版本号最大者。
type chain struct {
	byVer   map[uint64]*record
	current *record
}

// Bucket 是多租户对象存储桶。所有方法可并发调用。
type Bucket struct {
	mu      sync.Mutex
	mode    lock.Mode
	defRet  uint64
	lastNow uint64
	nextVer uint64
	chains  map[string]*chain
	audit   []AuditEntry
	touched int
}

// New 创建桶；mode 为 GOVERNANCE 或 COMPLIANCE，D 为默认保留秒数（0 表示无）。
func New(mode lock.Mode, d uint64) (*Bucket, *Error) {
	if mode != lock.Governance && mode != lock.Compliance {
		return nil, &Error{Kind: ErrInvalidParam, Index: -1}
	}
	if d > maxDef {
		return nil, &Error{Kind: ErrInvalidParam, Index: -1}
	}
	return &Bucket{mode: mode, defRet: d, nextVer: 1, chains: map[string]*chain{}}, nil
}

// Put 追加一个数据版本，返回版本号。
func (b *Bucket) Put(key string, size uint64, now uint64, op authz.Set) (uint64, *Error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if key == "" || size > maxSize || now > maxNow {
		return 0, errOf(ErrInvalidParam)
	}
	if now < b.lastNow {
		return 0, errOf(ErrClockRollback)
	}
	rec := &record{ver: b.nextVer, size: size}
	if b.defRet > 0 {
		rec.mode = b.mode
		rec.until = now + b.defRet
	}
	b.append(key, rec)
	b.lastNow = now
	return rec.ver, nil
}

// Delete 追加一个删除标记，返回版本号。
func (b *Bucket) Delete(key string, now uint64, op authz.Set) (uint64, *Error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if key == "" || now > maxNow {
		return 0, errOf(ErrInvalidParam)
	}
	if now < b.lastNow {
		return 0, errOf(ErrClockRollback)
	}
	rec := &record{ver: b.nextVer, marker: true}
	b.append(key, rec)
	b.lastNow = now
	return rec.ver, nil
}

// Get 返回当前数据版本；当前为标记报 ErrMarkedDeleted，无任何版本报 ErrNotExist。
func (b *Bucket) Get(key string) (*Info, *Error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if key == "" {
		return nil, errOf(ErrInvalidParam)
	}
	c := b.chains[key]
	if c == nil || c.current == nil {
		return nil, errOf(ErrNotExist)
	}
	b.touched++ // 只读当前版本这一条记录
	cur := c.current
	if cur.marker {
		return nil, errOf(ErrMarkedDeleted)
	}
	return &Info{
		Key: key, Ver: cur.ver, Size: cur.size,
		Mode: cur.mode, RetainUntil: cur.until, Hold: cur.hold,
	}, nil
}

// DeleteVersion 永久删除一个版本。
func (b *Bucket) DeleteVersion(key string, ver uint64, bypass bool, now uint64, op authz.Set) *Error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if key == "" || ver == 0 || now > maxNow {
		return errOf(ErrInvalidParam)
	}
	if now < b.lastNow {
		return errOf(ErrClockRollback)
	}
	if !op.Has(authz.DeleteVersion) {
		return errOf(ErrNoPermission)
	}
	c, rec := b.find(key, ver)
	if rec == nil {
		return errOf(ErrVersionNotFound)
	}
	if !rec.marker {
		if r := lock.CheckDelete(rec.hold, rec.mode, rec.until, now, bypass, op.Has(authz.BypassGovernance)); r != lock.Allow {
			return errOf(reasonKind(r))
		}
	}
	b.remove(c, rec, key, now)
	b.lastNow = now
	return nil
}

// SetRetention 设置或清除一个数据版本的保留期。
func (b *Bucket) SetRetention(key string, ver uint64, mode lock.Mode, until uint64, bypass bool, now uint64, op authz.Set) *Error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if key == "" || ver == 0 || now > maxNow || !mode.Valid() {
		return errOf(ErrInvalidParam)
	}
	if mode != lock.None && (until <= now || until > maxUntil) {
		return errOf(ErrInvalidParam)
	}
	if now < b.lastNow {
		return errOf(ErrClockRollback)
	}
	if !op.Has(authz.PutRetention) {
		return errOf(ErrNoPermission)
	}
	_, rec := b.find(key, ver)
	if rec == nil || rec.marker {
		return errOf(ErrVersionNotFound)
	}
	if r := lock.CheckSetRetention(rec.mode, rec.until, mode, until, now, bypass, op.Has(authz.BypassGovernance)); r != lock.Allow {
		return errOf(reasonKind(r))
	}
	if mode == lock.None {
		rec.mode, rec.until = lock.None, 0
	} else {
		rec.mode, rec.until = mode, until
	}
	b.lastNow = now
	return nil
}

// SetHold 设置或解除一个数据版本的法律保留。
func (b *Bucket) SetHold(key string, ver uint64, on bool, now uint64, op authz.Set) *Error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if key == "" || ver == 0 || now > maxNow {
		return errOf(ErrInvalidParam)
	}
	if now < b.lastNow {
		return errOf(ErrClockRollback)
	}
	if !op.Has(authz.PutRetention) {
		return errOf(ErrNoPermission)
	}
	_, rec := b.find(key, ver)
	if rec == nil || rec.marker {
		return errOf(ErrVersionNotFound)
	}
	rec.hold = on
	b.lastNow = now
	return nil
}

// DeleteVersions 全有或全无地批量永久删除。
func (b *Bucket) DeleteVersions(items []Item, bypass bool, now uint64, op authz.Set) *Error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// 批级检查：参数非法 > 时钟回退 > 无权限，均不带下标。
	if len(items) == 0 || len(items) > maxBatch || now > maxNow {
		return errOf(ErrInvalidParam)
	}
	seen := make(map[Item]struct{}, len(items))
	for _, it := range items {
		if it.Key == "" || it.Ver == 0 {
			return errOf(ErrInvalidParam)
		}
		if _, dup := seen[it]; dup {
			return errOf(ErrInvalidParam)
		}
		seen[it] = struct{}{}
	}
	if now < b.lastNow {
		return errOf(ErrClockRollback)
	}
	if !op.Has(authz.DeleteVersion) {
		return errOf(ErrNoPermission)
	}
	// 逐项校验：全部基于批开始时的状态（校验期间不改动任何状态）。
	type target struct {
		c   *chain
		rec *record
	}
	targets := make([]target, len(items))
	for i, it := range items {
		c, rec := b.find(it.Key, it.Ver)
		if rec == nil {
			return &Error{Kind: ErrVersionNotFound, Index: i}
		}
		if !rec.marker {
			if r := lock.CheckDelete(rec.hold, rec.mode, rec.until, now, bypass, op.Has(authz.BypassGovernance)); r != lock.Allow {
				return &Error{Kind: reasonKind(r), Index: i}
			}
		}
		targets[i] = target{c: c, rec: rec}
	}
	// 全部通过：按下标升序执行，经绕过者按此次序入审计。
	for i, it := range items {
		b.remove(targets[i].c, targets[i].rec, it.Key, now)
	}
	b.lastNow = now
	return nil
}

// append 把新版本接到键的链尾并成为当前版本，同时分配全桶序号。
func (b *Bucket) append(key string, rec *record) {
	c := b.chains[key]
	if c == nil {
		c = &chain{byVer: map[uint64]*record{}}
		b.chains[key] = c
	}
	if c.current != nil {
		rec.prev = c.current
		c.current.next = rec
	}
	c.current = rec
	c.byVer[rec.ver] = rec
	b.nextVer++
}

// find 按键与版本号定位记录。
func (b *Bucket) find(key string, ver uint64) (*chain, *record) {
	c := b.chains[key]
	if c == nil {
		return nil, nil
	}
	return c, c.byVer[ver]
}

// remove 摘除一个版本记录；必要时记入审计并重定当前版本。
func (b *Bucket) remove(c *chain, rec *record, key string, now uint64) {
	if !rec.marker && rec.mode == lock.Governance && now < rec.until {
		// 生效中的 GOVERNANCE 删除能成功，说明已经合法绕过，记入审计。
		b.audit = append(b.audit, AuditEntry{Key: key, Ver: rec.ver, Now: now, OldRetainUntil: rec.until})
	}
	if c.current == rec {
		b.touched++ // 被删的当前记录
		if rec.prev != nil {
			b.touched++ // 前驱即新当前版本
		}
		c.current = rec.prev
	}
	if rec.prev != nil {
		rec.prev.next = rec.next
	}
	if rec.next != nil {
		rec.next.prev = rec.prev
	}
	delete(c.byVer, rec.ver)
}

// Audit 返回按发生次序排列的审计条目副本。
func (b *Bucket) Audit() []AuditEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]AuditEntry, len(b.audit))
	copy(out, b.audit)
	return out
}
