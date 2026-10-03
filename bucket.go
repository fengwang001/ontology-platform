// Package bucket 在 version/lock/authz 之上编排多租户对象存储桶的全部规则。
package bucket

import (
	"errors"
	"strconv"

	"ontology/authz"
	"ontology/lock"
	"ontology/version"
)

// Mode 重导出 lock.Mode 作为桶 API。
type Mode = lock.Mode

const (
	NONE       = lock.NONE
	GOVERNANCE = lock.GOVERNANCE
	COMPLIANCE = lock.COMPLIANCE
)

var (
	ErrInvalidArg      = errors.New("参数非法")
	ErrClockRegression = errors.New("时钟回退")
	ErrPermission      = errors.New("无权限")
	ErrNotExist        = errors.New("版本不存在")
	ErrNoSuchKey       = errors.New("不存在")
	ErrDeleted         = errors.New("被标记删除")
	ErrLegalHold       = errors.New("法律保留开启")
	ErrCompliance      = errors.New("合规保留")
	ErrGovernance      = errors.New("治理保留")
)

// AuditEntry 记录一次因治理绕过而成功的永久删除。
type AuditEntry struct {
	Key      []byte
	Ver      int64
	Now      int64
	OldUntil int64
}

// Object 是 Get 成功时返回的数据版本。
type Object struct {
	Key  []byte
	Ver  int64
	Size int64
}

// Item 是批量删除中的一项。
type Item struct {
	Key []byte
	Ver int64
}

// BatchError 带批量内最小失败下标与原因。
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string {
	return "批量删除下标 " + strconv.Itoa(e.Index) + " 失败: " + e.Err.Error()
}

// Bucket 是多租户对象存储桶。
type Bucket struct {
	mode lock.Mode
	d    int64
	seq  int64
	now  int64
	vs   *version.Store
	aud  []AuditEntry
}

const (
	maxNow   = int64(1_000_000_000_000)
	maxSize  = int64(1_000_000_000_000)
	maxD     = int64(1_000_000_000)
	maxBatch = 1000
)

// New 创建桶。mode 必须为 GOVERNANCE/COMPLIANCE，D 必须在 [0,1e9]，
// 构造配置非法视为编程错误，直接 panic。
func New(mode Mode, d int64) *Bucket {
	if mode != GOVERNANCE && mode != COMPLIANCE {
		panic("bucket: mode 必须为 GOVERNANCE 或 COMPLIANCE")
	}
	if d < 0 || d > maxD {
		panic("bucket: D 超出 [0,1e9]")
	}
	return &Bucket{mode: mode, d: d, vs: version.NewStore()}
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// clockOK 仅检查回退不推进时钟；时钟只在操作真正被接受时由 commitClock 推进。
func (b *Bucket) clockOK(now int64) bool { return now >= b.now }

func (b *Bucket) commitClock(now int64) { b.now = now }

func (b *Bucket) nextVer() int64 {
	b.seq++
	return b.seq
}

// Put 写入数据版本，无权限要求。拒绝次序：参数非法 > 时钟回退。
func (b *Bucket) Put(_ authz.Operator, key []byte, size, now int64) (int64, error) {
	if len(key) == 0 || size < 0 || size > maxSize || !validNow(now) {
		return 0, ErrInvalidArg
	}
	b.vs.Lock()
	defer b.vs.Unlock()
	if !b.clockOK(now) {
		return 0, ErrClockRegression
	}
	ver := b.nextVer()
	b.vs.Append(key, size, false, ver)
	if b.d > 0 {
		r, _ := b.vs.Find(key, ver)
		r.Lock.Mode = b.mode
		r.Lock.Until = now + b.d
	}
	b.commitClock(now)
	return ver, nil
}

// Delete 追加删除标记，无权限要求、不受任何锁约束；当前已是标记也照加。
func (b *Bucket) Delete(_ authz.Operator, key []byte, now int64) (int64, error) {
	if len(key) == 0 || !validNow(now) {
		return 0, ErrInvalidArg
	}
	b.vs.Lock()
	defer b.vs.Unlock()
	if !b.clockOK(now) {
		return 0, ErrClockRegression
	}
	ver := b.nextVer()
	b.vs.Append(key, 0, true, ver)
	b.commitClock(now)
	return ver, nil
}

// Get 三态：数据版本 / 当前为删除标记 / 键不存在。
func (b *Bucket) Get(key []byte) (Object, error) {
	if len(key) == 0 {
		return Object{}, ErrInvalidArg
	}
	b.vs.Lock()
	defer b.vs.Unlock()
	r, ok := b.vs.Current(key)
	if !ok {
		return Object{}, ErrNoSuchKey
	}
	if r.Marker {
		return Object{}, ErrDeleted
	}
	return Object{Key: r.Key, Ver: r.Ver, Size: r.Size}, nil
}

// deleteCheck 基于当前状态判定一个永久删除是否放行。
// 按版本不存在 > 法律保留 > 合规保留 > 治理保留 次序返回第一个原因。
// 返回的 activeGov 表示“保留生效且为 GOVERNANCE”，audited 表示
// 本次确实是靠 bypass+BypassGovernance 才越过该治理保留（需入审计）。
func (b *Bucket) deleteCheck(key []byte, ver int64, op authz.Operator, bypass bool, now int64) (r version.Rec, audited bool, err error) {
	r, ok := b.vs.Find(key, ver)
	if !ok {
		return version.Rec{}, false, ErrNotExist
	}
	if r.Marker {
		return r, false, nil
	}
	if r.Lock.HasHold() {
		return r, false, ErrLegalHold
	}
	if r.Lock.Active(now) {
		switch r.Lock.Mode {
		case COMPLIANCE:
			return r, false, ErrCompliance
		case GOVERNANCE:
			if bypass && op.CanBypassGovernance() {
				return r, true, nil
			}
			return r, false, ErrGovernance
		}
	}
	return r, false, nil
}

// DeleteVersion 永久删除一个版本（标记永远可删）。
func (b *Bucket) DeleteVersion(op authz.Operator, key []byte, ver int64, bypass bool, now int64) error {
	if len(key) == 0 || ver <= 0 || !validNow(now) {
		return ErrInvalidArg
	}
	b.vs.Lock()
	defer b.vs.Unlock()
	if !b.clockOK(now) {
		return ErrClockRegression
	}
	if !op.CanDeleteVersion() {
		return ErrPermission
	}
	r, audited, err := b.deleteCheck(key, ver, op, bypass, now)
	if err != nil {
		return err
	}
	oldUntil := int64(0)
	if audited {
		oldUntil = r.Lock.Until
	}
	b.vs.Delete(key, ver)
	if audited {
		b.aud = append(b.aud, AuditEntry{Key: append([]byte(nil), key...), Ver: ver, Now: now, OldUntil: oldUntil})
	}
	b.commitClock(now)
	return nil
}

// SetRetention 设置/迁移保留期；拒绝次序：参数非法 > 时钟回退 > 无权限 >
// 版本不存在（标记同）> 合规保留/治理保留。
func (b *Bucket) SetRetention(op authz.Operator, key []byte, ver int64, mode Mode, until int64, bypass bool, now int64) error {
	if len(key) == 0 || ver <= 0 || !validNow(now) ||
		mode != NONE && mode != GOVERNANCE && mode != COMPLIANCE ||
		mode != NONE && (until <= now || until > maxNow) {
		return ErrInvalidArg
	}
	b.vs.Lock()
	defer b.vs.Unlock()
	if !b.clockOK(now) {
		return ErrClockRegression
	}
	if !op.CanPutRetention() {
		return ErrPermission
	}
	r, ok := b.vs.Find(key, ver)
	if !ok || r.Marker {
		return ErrNotExist
	}
	// 治理绕过必须同时满足调用方要求 bypass 且操作者持有 BypassGovernance。
	effectiveBypass := bypass && op.CanBypassGovernance()
	switch r.Lock.SetRetention(mode, until, now, effectiveBypass) {
	case lock.ErrCompliance:
		return ErrCompliance
	case lock.ErrGovernance:
		return ErrGovernance
	}
	b.commitClock(now)
	return nil
}

// SetHold 独立开关法律保留；标记按版本不存在。
func (b *Bucket) SetHold(op authz.Operator, key []byte, ver int64, on bool, now int64) error {
	if len(key) == 0 || ver <= 0 || !validNow(now) {
		return ErrInvalidArg
	}
	b.vs.Lock()
	defer b.vs.Unlock()
	if !b.clockOK(now) {
		return ErrClockRegression
	}
	if !op.CanPutRetention() {
		return ErrPermission
	}
	r, ok := b.vs.Find(key, ver)
	if !ok || r.Marker {
		return ErrNotExist
	}
	r.Lock.SetHold(on)
	b.commitClock(now)
	return nil
}

// DeleteVersions 全有或全无批量永久删除。批级检查先于逐项，
// 每项基于批开始状态判定；全部通过才按下标升序执行。
func (b *Bucket) DeleteVersions(op authz.Operator, items []Item, bypass bool, now int64) error {
	if len(items) == 0 || len(items) > maxBatch || !validNow(now) {
		return ErrInvalidArg
	}
	for _, it := range items {
		if len(it.Key) == 0 || it.Ver <= 0 {
			return ErrInvalidArg
		}
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		// 长度前缀编码保证不同 (key,ver) 不会碰撞。
		k := strconv.Itoa(len(it.Key)) + ":" + string(it.Key) + "#" + strconv.FormatInt(it.Ver, 10)
		if seen[k] {
			return ErrInvalidArg
		}
		seen[k] = true
	}
	b.vs.Lock()
	defer b.vs.Unlock()
	if !b.clockOK(now) {
		return ErrClockRegression
	}
	if !op.CanDeleteVersion() {
		return ErrPermission
	}
	type plan struct {
		key     []byte
		ver     int64
		audited bool
		until   int64
	}
	plans := make([]plan, len(items))
	for i, it := range items {
		r, audited, err := b.deleteCheck(it.Key, it.Ver, op, bypass, now)
		if err != nil {
			return &BatchError{Index: i, Err: err}
		}
		plans[i] = plan{key: it.Key, ver: it.Ver, audited: audited, until: r.Lock.Until}
	}
	for _, p := range plans {
		b.vs.Delete(p.key, p.ver)
		if p.audited {
			b.aud = append(b.aud, AuditEntry{Key: append([]byte(nil), p.key...), Ver: p.ver, Now: now, OldUntil: p.until})
		}
	}
	b.commitClock(now)
	return nil
}

// Audit 返回审计副本。
func (b *Bucket) Audit() []AuditEntry {
	b.vs.Lock()
	defer b.vs.Unlock()
	out := make([]AuditEntry, len(b.aud))
	copy(out, b.aud)
	return out
}
