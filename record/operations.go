package record

import "sort"

// AddUser 建立用户。
func (s *Store) AddUser(user, dept string, level int, roles []string) error {
	if user == "" || dept == "" || level < 1 || level > 3 {
		return ErrInvalid
	}
	roleSet := map[string]bool{}
	for _, r := range roles {
		if r != RoleArchivist && r != RoleMedical && r != RoleRecords {
			return ErrInvalid
		}
		roleSet[r] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.users[user] != nil {
		return ErrNotFound
	}
	s.users[user] = &User{Name: user, Dept: dept, Level: level, Roles: roleSet}
	return nil
}

// OpenEnc 建立就诊。
func (s *Store) OpenEnc(enc string) error {
	if enc == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.encs[enc] != nil {
		return ErrNotFound
	}
	s.encs[enc] = &Encounter{ID: enc, Discharge: -1}
	return nil
}

// Discharge 记录出院时刻。
func (s *Store) Discharge(now int64, enc string) error {
	if !validTS(now) || enc == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	e := s.encs[enc]
	if e == nil {
		return ErrNotFound
	}
	if e.Discharged {
		return ErrState
	}
	e.Discharged = true
	e.Discharge = now
	s.maxNow = now
	return nil
}

// Create 建立文档第 1 版。
func (s *Store) Create(now int64, user, enc, doc, content string) error {
	if !validTS(now) || user == "" || enc == "" || doc == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	u := s.users[user]
	e := s.encs[enc]
	if u == nil || e == nil {
		return ErrNotFound
	}
	if s.docs[doc] != nil {
		return ErrNotFound
	}
	snap, h0 := s.snapshotAll(), s.hashes
	s.sweepLocked(now)
	if e.Discharged && now >= e.Discharge+s.T {
		s.rollbackTx(snap, h0)
		return ErrState
	}
	d := &Doc{ID: doc, Enc: enc, Author: user, Status: StatusDraft}
	s.docs[doc] = d
	s.AppendVersion(d, now, user, content)
	s.maxNow = now
	return nil
}

// Edit 追加新版本：仅作者、仅未封存（或已过审签则报状态不符）。
func (s *Store) Edit(now int64, user, doc, content string) error {
	if !validTS(now) || user == "" || doc == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	u := s.users[user]
	d := s.docs[doc]
	if u == nil || d == nil {
		return ErrNotFound
	}
	if u.Name != d.Author {
		return ErrPermission
	}
	snap, h0 := s.snapshotAll(), s.hashes
	s.sweepLocked(now)
	if s.effectiveSealed(d, now) {
		s.rollbackTx(snap, h0)
		return ErrState
	}
	if d.Status == StatusCosigned {
		s.rollbackTx(snap, h0)
		return ErrState
	}
	s.AppendVersion(d, now, user, content)
	if d.Status == StatusSigned {
		s.ResetSignatures(d)
	}
	s.maxNow = now
	return nil
}

func (s *Store) snapshotAll() map[string]*Doc {
	snap := map[string]*Doc{}
	for _, d := range s.docs {
		snap[d.ID] = cloneDoc(d)
	}
	return snap
}

// Defects 返回封存而未到已审签的缺陷文档号（字节序）。
func (s *Store) Defects(now int64) ([]string, error) {
	if !validTS(now) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return nil, ErrClock
	}
	var out []string
	for _, d := range s.docs {
		if s.effectiveSealed(d, now) && d.Status != StatusCosigned {
			out = append(out, d.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// VerifyResult 为校验结果。
type VerifyResult struct {
	VersionChainOK bool
	AmendChainOK   bool
	SealHash       []byte
	Gen            int
}

// Verify 重算全部哈希：版本链、补记链与当前 sealHash。
func (s *Store) Verify(doc string) (VerifyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[doc]
	if d == nil {
		return VerifyResult{}, ErrNotFound
	}
	okV := true
	prev := ZeroHash
	for _, v := range d.Vers {
		got := s.hashOnce(prev, v.TS, v.Author, v.Content)
		if !equalBytes(got, v.Hash) {
			okV = false
		}
		prev = v.Hash
	}
	okA := true
	prev = ZeroHash
	for _, a := range d.Amends {
		got := s.hashOnce(prev, a.TS, a.Author, a.Content)
		if !equalBytes(got, a.Hash) {
			okA = false
		}
		prev = a.Hash
	}
	res := VerifyResult{VersionChainOK: okV, AmendChainOK: okA, Gen: d.Gen}
	head := ZeroHash
	if n := len(d.Vers); n > 0 {
		head = d.Vers[n-1].Hash
	}
	seal := s.sealHashOnce(head, d.Gen)
	if d.Gen > 0 {
		res.SealHash = seal
	}
	return res, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RunOp 供 sign 包编排：持锁、时钟/存在性、试 sweep、判定、提交/回滚。
func (s *Store) RunOp(now int64, user, doc string, fn func(u *User, d *Doc) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runOp(now, user, doc, fn)
}
