package record

import "sort"

// ZeroHash 为 32 个零字节，版本链/补记链的 h_0/a_0。
var ZeroHash = make([]byte, 32)

// MaxNow 返回已接受操作的最大 now。
func (s *Store) MaxNow() int64 { return s.maxNow }

// Hashes 返回累计哈希计算次数（非导出计数器的测试访问器）。
func (s *Store) Hashes() int { return s.hashes }

// User 返回用户副本。
func (s *Store) User(name string) (User, bool) {
	u, ok := s.users[name]
	if !ok {
		return User{}, false
	}
	roles := map[string]bool{}
	for k, v := range u.Roles {
		roles[k] = v
	}
	return User{Name: u.Name, Dept: u.Dept, Level: u.Level, Roles: roles}, true
}

// EncounterInfo 返回就诊信息：id、出院时刻、是否已出院。
func (s *Store) EncounterInfo(enc string) (Encounter, bool) {
	e, ok := s.encs[enc]
	if !ok {
		return Encounter{}, false
	}
	return *e, true
}

// DocInfo 返回文档状态快照。
func (s *Store) DocInfo(doc string) (DocInfo, bool) {
	d, ok := s.docs[doc]
	if !ok {
		return DocInfo{}, false
	}
	return d.info(), true
}

// DocInfo 是文档的只读快照。
type DocInfo struct {
	ID           string
	Enc          string
	Author       string
	Status       int
	VersionCount int
	AmendCount   int
	Sealed       bool // 有效封存位（窗口结束已自动再封存）
	Gen          int
	SealHash     []byte
	WindowOpen   bool
	UnsealEnd    int64
	SealTS       int64
	LastSealTS   int64
	SignTS       int64
	SignLate     bool
	CosignTS     int64
	CosignBy     string
	CosignLate   bool
	LatestHash   []byte
}

func (d *Doc) info() DocInfo {
	latest := append([]byte(nil), ZeroHash...)
	if n := len(d.Vers); n > 0 {
		latest = append([]byte(nil), d.Vers[n-1].Hash...)
	}
	sealTS := d.LastSealTS
	return DocInfo{
		ID: d.ID, Enc: d.Enc, Author: d.Author, Status: d.Status,
		VersionCount: len(d.Vers), AmendCount: len(d.Amends),
		Sealed:     d.Sealed,
		Gen:        d.Gen,
		SealHash:   append([]byte(nil), d.SealHash...),
		WindowOpen: d.WindowOpen,
		UnsealEnd:  d.UnsealEnd,
		SealTS:     sealTS,
		LastSealTS: d.LastSealTS,
		SignTS:     d.SignTS, SignLate: d.SignLate,
		CosignTS: d.CosignTS, CosignBy: d.CosignBy, CosignLate: d.CosignLate,
		LatestHash: latest,
	}
}

func cloneDoc(d *Doc) *Doc {
	c := *d
	c.Vers = append([]Version(nil), d.Vers...)
	c.Amends = append([]Amendment(nil), d.Amends...)
	c.SealHash = append([]byte(nil), d.SealHash...)
	return &c
}

// locked 下执行：时钟检查 + 实体检索。
func (s *Store) preamble(now int64, user, doc string) (*User, *Doc, *Encounter, error) {
	if !validTS(now) {
		return nil, nil, nil, ErrInvalid
	}
	if now < s.maxNow {
		return nil, nil, nil, ErrClock
	}
	var u *User
	if user != "" {
		u = s.users[user]
		if u == nil {
			return nil, nil, nil, ErrNotFound
		}
	}
	var d *Doc
	if doc != "" {
		d = s.docs[doc]
		if d == nil {
			return nil, nil, nil, ErrNotFound
		}
	}
	var e *Encounter
	if d != nil {
		e = s.encs[d.Enc]
	}
	return u, d, e, nil
}

// effectiveSealed 判定文档当前是否处于有效封存状态。
func (s *Store) effectiveSealed(d *Doc, now int64) bool {
	if !d.Sealed {
		return false
	}
	if d.WindowOpen && now < d.UnsealEnd {
		return false
	}
	return true
}

// applySeal 在给定时刻对一份文档执行一次封存（调用方保证确实需要封存）。
func (s *Store) applySeal(d *Doc, now int64) {
	d.Sealed = true
	d.WindowOpen = false
	d.UnsealStart, d.UnsealEnd = 0, 0
	d.Gen++
	head := ZeroHash
	if n := len(d.Vers); n > 0 {
		head = d.Vers[n-1].Hash
	}
	d.SealHash = s.sealHashOnce(head, d.Gen)
	d.SealTS = now
	d.LastSealTS = now
}

// dueEvents 收集 now 时刻应落地的封存事件（窗口结束再封存优先于出院封存，二者不会并存）。
func (s *Store) dueEvents(now int64) []*Doc {
	var due []*Doc
	for _, d := range s.docs {
		if d.Sealed && d.WindowOpen && now >= d.UnsealEnd {
			due = append(due, d)
			continue
		}
		if !d.Sealed {
			e := s.encs[d.Enc]
			if e != nil && e.Discharged && now >= e.Discharge+s.T {
				due = append(due, d)
			}
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID < due[j].ID })
	return due
}

// sweepLocked 在 now 落地全部到期封存。
func (s *Store) sweepLocked(now int64) {
	for _, d := range s.dueEvents(now) {
		s.applySeal(d, now)
	}
}

// runOp 是 sign 类操作的通用事务框架：
// 时钟与存在性检查 → 试落地到期封存（回滚）→ fn 判定与变更 → 提交并推进时钟。
func (s *Store) runOp(now int64, user, doc string, fn func(u *User, d *Doc) error) error {
	if !validTS(now) {
		return ErrInvalid
	}
	snap, h0, err := s.beginTx(now, user, doc)
	if err != nil {
		return err
	}
	s.sweepLocked(now)
	u := s.users[user]
	d := s.docs[doc]
	if err := fn(u, d); err != nil {
		s.rollbackTx(snap, h0)
		return err
	}
	s.maxNow = now
	return nil
}

func (s *Store) beginTx(now int64, user, doc string) (map[string]*Doc, int, error) {
	if now < s.maxNow {
		return nil, 0, ErrClock
	}
	if user != "" && s.users[user] == nil {
		return nil, 0, ErrNotFound
	}
	if doc != "" && s.docs[doc] == nil {
		return nil, 0, ErrNotFound
	}
	snap := map[string]*Doc{}
	for _, d := range s.docs {
		snap[d.ID] = cloneDoc(d)
	}
	return snap, s.hashes, nil
}

func (s *Store) rollbackTx(snap map[string]*Doc, h0 int) {
	s.docs = snap
	s.hashes = h0
}

// Lock / Unlock 供 sign、seal 包自行编排事务。
func (s *Store) Lock()                       { s.mu.Lock() }
func (s *Store) Unlock()                     { s.mu.Unlock() }
func (s *Store) Sweep(now int64)             { s.sweepLocked(now) }
func (s *Store) GetUser(name string) *User   { return s.users[name] }
func (s *Store) GetDoc(id string) *Doc       { return s.docs[id] }
func (s *Store) GetEnc(id string) *Encounter { return s.encs[id] }

// BeginTx/CommitTx/RollbackTx 供 seal 包使用。
func (s *Store) BeginTx(now int64, user, doc string) (*User, *Doc, map[string]*Doc, int, error) {
	if !validTS(now) {
		return nil, nil, nil, 0, ErrInvalid
	}
	snap, h0, err := s.beginTx(now, user, doc)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	var u *User
	if user != "" {
		u = s.users[user]
	}
	var d *Doc
	if doc != "" {
		d = s.docs[doc]
	}
	return u, d, snap, h0, nil
}

// Commit 提交事务并推进时钟。
func (s *Store) Commit(now int64) { s.maxNow = now }

// Rollback 回滚试落地的封存与哈希计数。
func (s *Store) Rollback(snap map[string]*Doc, h0 int) { s.rollbackTx(snap, h0) }

// BeginTxMulti 供 Unseal 使用：校验多个用户与文档均存在。
// 返回的 users 仅包含请求中列出的用户。
func (s *Store) BeginTxMulti(now int64, doc string, users ...string) (map[string]*User, *Doc, map[string]*Doc, int, error) {
	if !validTS(now) {
		return nil, nil, nil, 0, ErrInvalid
	}
	if now < s.maxNow {
		return nil, nil, nil, 0, ErrClock
	}
	um := map[string]*User{}
	for _, name := range users {
		u := s.users[name]
		if u == nil {
			return nil, nil, nil, 0, ErrNotFound
		}
		um[name] = u
	}
	d := s.docs[doc]
	if d == nil {
		return nil, nil, nil, 0, ErrNotFound
	}
	snap := s.snapshotAll()
	return um, d, snap, s.hashes, nil
}

// EffectiveSealed 报告文档在 now 的有效封存状态（调用方持锁）。
func (s *Store) EffectiveSealed(d *Doc, now int64) bool { return s.effectiveSealed(d, now) }

// ---- 落地原语（调用方持锁、已完成全部判定） ----

// AppendVersion 追加一个文档版本。
func (s *Store) AppendVersion(d *Doc, now int64, author, content string) {
	prev := ZeroHash
	if n := len(d.Vers); n > 0 {
		prev = d.Vers[n-1].Hash
	}
	d.Vers = append(d.Vers, Version{TS: now, Author: author, Content: content, Hash: s.hashOnce(prev, now, author, content)})
}

// AppendAmendment 追加一条补记。
func (s *Store) AppendAmendment(d *Doc, now int64, author, content string) {
	prev := ZeroHash
	if n := len(d.Amends); n > 0 {
		prev = d.Amends[n-1].Hash
	}
	d.Amends = append(d.Amends, Amendment{TS: now, Author: author, Content: content, Hash: s.hashOnce(prev, now, author, content)})
}

// ApplySeal 执行一次封存（自动或手动）。
func (s *Store) ApplySeal(d *Doc, now int64) { s.applySeal(d, now) }

// MarkSigned 落地签名；level>=2 的作者直接成为已审签。
func (s *Store) MarkSigned(d *Doc, u *User, now int64, sealed bool) {
	d.SignTS, d.SignLate = now, sealed
	if u.Level >= 2 {
		d.Status = StatusCosigned
	} else {
		d.Status = StatusSigned
	}
}

// MarkCosigned 落地上级审签。
func (s *Store) MarkCosigned(d *Doc, u *User, now int64, sealed bool) {
	d.Status = StatusCosigned
	d.CosignTS, d.CosignBy, d.CosignLate = now, u.Name, sealed
}

// ResetSignatures 清空签名并退回草稿。
func (s *Store) ResetSignatures(d *Doc) {
	d.Status = StatusDraft
	d.SignTS, d.SignLate = 0, false
	d.CosignTS, d.CosignBy, d.CosignLate = 0, "", false
}

// OpenUnsealWindow 开启解封窗口。
func (s *Store) OpenUnsealWindow(d *Doc, now int64) {
	d.WindowOpen = true
	d.UnsealStart = now
	d.UnsealEnd = now + s.U
}

// Node 是一条版本或补记的只读信息。
type Node struct {
	TS      int64
	Author  string
	Content string
	Hash    []byte
}

// Snapshot 是一份文档的完整只读快照。
type Snapshot struct {
	ID         string
	Enc        string
	Author     string
	Status     int
	Vers       []Node
	Amends     []Node
	Sealed     bool
	Gen        int
	SealHash   []byte
	WindowOpen bool
	UnsealEnd  int64
	SignTS     int64
	SignLate   bool
	CosignTS   int64
	CosignBy   string
	CosignLate bool
}

// Snapshot 返回文档完整快照（含每版哈希），供校验与测试对照。
func (s *Store) Snapshot(doc string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[doc]
	if d == nil {
		return Snapshot{}, false
	}
	out := Snapshot{
		ID: d.ID, Enc: d.Enc, Author: d.Author, Status: d.Status,
		Sealed: d.Sealed, Gen: d.Gen,
		SealHash:   append([]byte(nil), d.SealHash...),
		WindowOpen: d.WindowOpen, UnsealEnd: d.UnsealEnd,
		SignTS: d.SignTS, SignLate: d.SignLate,
		CosignTS: d.CosignTS, CosignBy: d.CosignBy, CosignLate: d.CosignLate,
	}
	for _, v := range d.Vers {
		out.Vers = append(out.Vers, Node{TS: v.TS, Author: v.Author, Content: v.Content, Hash: append([]byte(nil), v.Hash...)})
	}
	for _, a := range d.Amends {
		out.Amends = append(out.Amends, Node{TS: a.TS, Author: a.Author, Content: a.Content, Hash: append([]byte(nil), a.Hash...)})
	}
	return out, true
}

// DocIDs 返回全部文档号（字节序）。
func (s *Store) DocIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id := range s.docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// EncDischarge 返回就诊出院时刻；未出院第二个返回值为 false。
func (s *Store) EncDischarge(enc string) (int64, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.encs[enc]
	if e == nil {
		return 0, false, false
	}
	return e.Discharge, e.Discharged, true
}
