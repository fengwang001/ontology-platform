// Package record 管理就诊文档的版本与哈希链。
package record

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"sync"
)

// 统一的拒绝原因，严格按 参数 > 时钟 > 不存在 > 无权限 > 状态 的次序返回。
var (
	ErrInvalid = errInvalid{}
	ErrClock   = errClock{}
	ErrMissing = errMissing{}
	ErrPerm    = errPerm{}
	ErrState   = errState{}
)

type errInvalid struct{}
type errClock struct{}
type errMissing struct{}
type errPerm struct{}
type errState struct{}

func (errInvalid) Error() string { return "invalid argument" }
func (errClock) Error() string   { return "clock moved backwards" }
func (errMissing) Error() string { return "user, encounter or document not found" }
func (errPerm) Error() string    { return "permission denied" }
func (errState) Error() string   { return "state mismatch" }

// 文档签名状态。
const (
	StateDraft = iota // 草稿
	StateSigned       // 已签
	StateApproved     // 已审签
)

// 用户角色位。
const (
	RoleArchivist = 1 << iota // 归档员
	RoleMedicalApproval       // 医务审批
	RoleRecordApproval        // 病案审批
)

// User 为系统用户。
type User struct {
	Name  string
	Dept  string
	Level int
	Roles int
}

// Enc 为一次就诊。
type Enc struct {
	ID         string
	Discharged bool
	Dis        int64
}

// Version 是文档的一个不可变版本。
type Version struct {
	TS      int64
	Author  string
	Content []byte
	Hash    []byte
}

// Signature 记录一次签名/审签。
type Signature struct {
	TS     int64
	User   string
	Late   bool
	Cosign bool
}

// AmendEntry 是封存后补记链上的一条。
type AmendEntry struct {
	TS      int64
	User    string
	Content []byte
	Hash    []byte
}

// Doc 是一份文档的全部状态。
type Doc struct {
	ID     string
	Enc    string
	Author string

	Versions []Version
	State    int // StateDraft / StateSigned / StateApproved

	Sealed   bool
	Gen      int64
	SealHash []byte

	// UnsealedUntil 为解封窗口截止时刻；> 0 表示文档当前处于解封窗口内（按未封存对待）。
	UnsealedUntil int64

	AuthorSigned bool
	LateSign     []Signature
	Defect       bool

	Amends []AmendEntry
}

// SealEvent 为待落地封存事件，供上层包使用。
type SealEvent struct {
	At   int64
	Doc  *Doc
	Kind int // 0 出院后自动封存，1 解封窗口到期再封存
}

// sealEvent 是内部封存事件，附带文档号用于排序。
type sealEvent struct {
	at   int64
	doc  *Doc
	kind int
	id   string
}

// Record 为底层服务。
type Record struct {
	mu     sync.Mutex
	t      int64
	u      int64
	nowMax int64
	hasNow bool
	users  map[string]*User
	encs   map[string]*Enc
	docs   map[string]*Doc
	hashes int
}

// New 创建服务：T 为出院后自动封存时限，U 为解封窗口（分钟）。
func New(T, U int64) *Record {
	if T < 1 || T > 1_000_000 || U < 1 || U > 1_000_000 {
		panic("record: T and U must be within [1, 10^6]")
	}
	return &Record{t: T, u: U, users: map[string]*User{}, encs: map[string]*Enc{}, docs: map[string]*Doc{}}
}

// HashCount 返回累计哈希计算次数（测试用）。
func (r *Record) HashCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hashes
}

func validTS(now int64) bool { return now >= 0 && now <= 1_000_000_000 }

func validName(s string) bool { return len(s) > 0 && len(s) <= math.MaxUint32 }

// AddUser 登记用户。
func (r *Record) AddUser(user, dept string, level int, roles int) error {
	if !validName(user) || !validName(dept) || level < 1 || level > 3 ||
		roles < 0 || roles&^(RoleArchivist|RoleMedicalApproval|RoleRecordApproval) != 0 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[user]; ok {
		return ErrInvalid
	}
	r.users[user] = &User{Name: user, Dept: dept, Level: level, Roles: roles}
	return nil
}

// OpenEnc 建立就诊。
func (r *Record) OpenEnc(enc string) error {
	if !validName(enc) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.encs[enc]; ok {
		return ErrInvalid
	}
	r.encs[enc] = &Enc{ID: enc}
	return nil
}

// Discharge 记录出院时刻。
func (r *Record) Discharge(now int64, enc string) error {
	if !validTS(now) || !validName(enc) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasNow && now < r.nowMax {
		return ErrClock
	}
	e, ok := r.encs[enc]
	if !ok {
		return ErrMissing
	}
	if e.Discharged {
		return ErrState
	}
	e.Discharged = true
	e.Dis = now
	r.advance(now)
	return nil
}

// Create 建立文档第 1 版。
func (r *Record) Create(now int64, user, enc, doc string, content []byte) error {
	if !validTS(now) || !validName(user) || !validName(enc) || !validName(doc) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasNow && now < r.nowMax {
		return ErrClock
	}
	e, ok := r.encs[enc]
	if !ok {
		return ErrMissing
	}
	if _, ok := r.users[user]; !ok {
		return ErrMissing
	}
	if _, ok := r.docs[doc]; ok {
		return ErrMissing
	}
	// 先落地到期封存，再判断就诊是否已到封存时刻。
	r.commit(r.planSealEvents(now))
	if e.Discharged && now >= e.Dis+r.t {
		return ErrState
	}
	h := r.hashNext(make([]byte, 32), now, user, content)
	r.docs[doc] = &Doc{
		ID:       doc,
		Enc:      enc,
		Author:   user,
		Versions: []Version{{TS: now, Author: user, Content: append([]byte(nil), content...), Hash: h}},
	}
	r.advance(now)
	return nil
}

// Edit 追加新版本；仅作者、仅草稿、仅未封存时可做。
func (r *Record) Edit(now int64, user, doc string, content []byte) error {
	if !validTS(now) || !validName(user) || !validName(doc) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasNow && now < r.nowMax {
		return ErrClock
	}
	d, ok := r.docs[doc]
	if !ok {
		return ErrMissing
	}
	if _, ok := r.users[user]; !ok {
		return ErrMissing
	}
	if user != d.Author {
		return ErrPerm
	}
	r.commit(r.planSealEvents(now))
	if EffectiveSealed(d) || d.State == StateApproved {
		return ErrState
	}
	last := d.Versions[len(d.Versions)-1].Hash
	h := r.hashNext(last, now, user, content)
	d.Versions = append(d.Versions, Version{TS: now, Author: user, Content: append([]byte(nil), content...), Hash: h})
	// 已签下追加：签名失效、退回草稿。
	d.State = StateDraft
	d.AuthorSigned = false
	r.advance(now)
	return nil
}

// Inspect 返回文档状态快照（只读查询，测试用）。
func (r *Record) Inspect(doc string) (Doc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.docs[doc]
	if !ok {
		return Doc{}, ErrMissing
	}
	return *d, nil
}

// Snapshot 返回按字节序排列的全部文档号。
func (r *Record) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.docs))
	for id := range r.docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// IDsLocked 在持锁状态下返回按字节序排列的全部文档号。
func (r *Record) IDsLocked() []string {
	ids := make([]string, 0, len(r.docs))
	for id := range r.docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// BumpHash 在持锁状态下计入一次哈希计算。
func (r *Record) BumpHash() { r.hashes++ }

// Lookup 在持锁状态下取文档内部对象。
func (r *Record) Lookup(doc string) *Doc { return r.docs[doc] }

// UserOf 在持锁状态下取用户。
func (r *Record) UserOf(name string) *User { return r.users[name] }

// Lock / Unlock 供上层包复用同一把锁。
func (r *Record) Lock()   { r.mu.Lock() }
func (r *Record) Unlock() { r.mu.Unlock() }

// HasNow 在持锁状态下报告时钟是否已推进。
func (r *Record) HasNow() bool { return r.hasNow }

// NowMax 返回已接受操作的最大 now。
func (r *Record) NowMax() int64 { return r.nowMax }

// Advance 在持锁状态下推进时钟。
func (r *Record) Advance(now int64) { r.advance(now) }

// Plan 在持锁状态下规划封存事件。
func (r *Record) Plan(now int64) []SealEvent {
	internal := r.planSealEvents(now)
	out := make([]SealEvent, len(internal))
	for i, ev := range internal {
		out[i] = SealEvent{At: ev.at, Doc: ev.doc, Kind: ev.kind}
	}
	return out
}

// Commit 在持锁状态下落地封存事件。
func (r *Record) Commit(evs []SealEvent) {
	for i := range evs {
		r.applySeal(evs[i].Doc, evs[i].At)
	}
}

// ApplySeal 在持锁状态下对文档执行一次封存。
func (r *Record) ApplySeal(d *Doc, at int64) { r.applySeal(d, at) }

// T 返回自动封存时限。
func (r *Record) T() int64 { return r.t }

// U 返回解封窗口。
func (r *Record) U() int64 { return r.u }

// HashNext 计算版本/补记链的下一个哈希并计数。
func (r *Record) HashNext(prev []byte, ts int64, author string, content []byte) []byte {
	return r.hashNext(prev, ts, author, content)
}

func (r *Record) advance(now int64) {
	if !r.hasNow || now > r.nowMax {
		r.nowMax = now
		r.hasNow = true
	}
}

func (r *Record) hashNext(prev []byte, ts int64, author string, content []byte) []byte {
	r.hashes++
	var lenbuf [4]byte
	binary.BigEndian.PutUint32(lenbuf[:], uint32(len(author)))
	var tsbuf [8]byte
	binary.BigEndian.PutUint64(tsbuf[:], uint64(ts))
	h := sha256.New()
	h.Write(prev)
	h.Write(tsbuf[:])
	h.Write(lenbuf[:])
	h.Write([]byte(author))
	h.Write(content)
	return h.Sum(nil)
}

// planSealEvents 计算 now 之前（含恰等）应落地的封存事件，按 (时刻, 文档号) 排序。
func (r *Record) planSealEvents(now int64) []sealEvent {
	var evs []sealEvent
	for id, d := range r.docs {
		if d.UnsealedUntil > 0 {
			if now >= d.UnsealedUntil {
				evs = append(evs, sealEvent{at: d.UnsealedUntil, doc: d, kind: 1, id: id})
			}
			continue
		}
		e := r.encs[d.Enc]
		if !d.Sealed && e.Discharged && now >= e.Dis+r.t {
			evs = append(evs, sealEvent{at: e.Dis + r.t, doc: d, kind: 0, id: id})
		}
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].at != evs[j].at {
			return evs[i].at < evs[j].at
		}
		return evs[i].id < evs[j].id
	})
	return evs
}

func (r *Record) commit(evs []sealEvent) {
	for i := range evs {
		r.applySeal(evs[i].doc, evs[i].at)
	}
}

func (r *Record) applySeal(d *Doc, at int64) {
	d.UnsealedUntil = 0
	d.Sealed = true
	d.Gen++
	var genbuf [8]byte
	binary.BigEndian.PutUint64(genbuf[:], uint64(d.Gen))
	h := sha256.New()
	h.Write(d.Versions[len(d.Versions)-1].Hash)
	h.Write(genbuf[:])
	d.SealHash = h.Sum(nil)
	r.hashes++
	if d.State != StateApproved {
		d.Defect = true
	}
}

// EffectiveSealed 报告文档当前是否按封存对待（处于解封窗口时按未封存）。
func EffectiveSealed(d *Doc) bool {
	return d.Sealed && d.UnsealedUntil == 0
}
