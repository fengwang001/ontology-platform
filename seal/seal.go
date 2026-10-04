// Package seal 提供封存、限时解封、封存后补记与校验。
package seal

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"ontology/record"
)

// VerifyResult 为 Verify 的结果。
type VerifyResult struct {
	VersionChainOK bool
	AmendChainOK   bool
	SealHash       []byte
}

// Seal 服务。
type Seal struct {
	*record.Record
}

// New 创建封存服务：T 为出院后自动封存时限，U 为解封窗口。
func New(T, U int64) *Seal {
	return &Seal{Record: record.New(T, U)}
}

// Wrap 在已有 record 存储上组装封存服务，使三个包共享同一状态与锁。
func Wrap(r *record.Record) *Seal { return &Seal{Record: r} }

// Seal 由归档员手动封存，要求已审签。
func (s *Seal) Seal(now int64, user, doc string) error {
	if now < 0 || now > 1_000_000_000 || user == "" || doc == "" {
		return record.ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	if s.HasNow() && now < s.NowMax() {
		return record.ErrClock
	}
	d := s.Lookup(doc)
	if d == nil {
		return record.ErrMissing
	}
	u := s.UserOf(user)
	if u == nil {
		return record.ErrMissing
	}
	if u.Roles&record.RoleArchivist == 0 {
		return record.ErrPerm
	}
	s.Commit(s.Plan(now))
	if record.EffectiveSealed(d) || d.State != record.StateApproved {
		return record.ErrState
	}
	s.ApplySeal(d, now)
	s.Advance(now)
	return nil
}

// Amend 封存后补记；作者本人或同科室 level>=2 者可做，另成一条链。
func (s *Seal) Amend(now int64, user, doc string, content []byte) error {
	if now < 0 || now > 1_000_000_000 || user == "" || doc == "" {
		return record.ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	if s.HasNow() && now < s.NowMax() {
		return record.ErrClock
	}
	d := s.Lookup(doc)
	if d == nil {
		return record.ErrMissing
	}
	u := s.UserOf(user)
	if u == nil {
		return record.ErrMissing
	}
	author := s.UserOf(d.Author)
	if author == nil || (user != d.Author && (u.Dept != author.Dept || u.Level < 2)) {
		return record.ErrPerm
	}
	s.Commit(s.Plan(now))
	if !record.EffectiveSealed(d) {
		return record.ErrState
	}
	var prev []byte
	if n := len(d.Amends); n > 0 {
		prev = d.Amends[n-1].Hash
	} else {
		prev = make([]byte, 32)
	}
	h := s.HashNext(prev, now, user, content)
	d.Amends = append(d.Amends, record.AmendEntry{
		TS: now, User: user, Content: append([]byte(nil), content...), Hash: h,
	})
	s.Advance(now)
	return nil
}

// Unseal 限时解封：a 须有医务审批、b 须有病案审批且二者不同人。
func (s *Seal) Unseal(now int64, a, b, doc string) error {
	if now < 0 || now > 1_000_000_000 || a == "" || b == "" || doc == "" {
		return record.ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	if s.HasNow() && now < s.NowMax() {
		return record.ErrClock
	}
	d := s.Lookup(doc)
	if d == nil {
		return record.ErrMissing
	}
	ua := s.UserOf(a)
	ub := s.UserOf(b)
	if ua == nil || ub == nil {
		return record.ErrMissing
	}
	if a == b || ua.Roles&record.RoleMedicalApproval == 0 || ub.Roles&record.RoleRecordApproval == 0 {
		return record.ErrPerm
	}
	s.Commit(s.Plan(now))
	if !record.EffectiveSealed(d) {
		return record.ErrState
	}
	d.UnsealedUntil = now + s.U()
	s.Advance(now)
	return nil
}

// Defects 返回当前按封存对待且未到已审签的缺陷文档号（按字节序）。
func (s *Seal) Defects() []string {
	s.Lock()
	defer s.Unlock()
	var out []string
	for _, id := range s.IDsLocked() {
		d := s.Lookup(id)
		if record.EffectiveSealed(d) && d.State != record.StateApproved {
			out = append(out, id)
		}
	}
	return out
}

// Verify 重算版本链、补记链与 sealHash。
func (s *Seal) Verify(doc string) (VerifyResult, error) {
	s.Lock()
	defer s.Unlock()
	d := s.Lookup(doc)
	if d == nil {
		return VerifyResult{}, record.ErrMissing
	}
	res := VerifyResult{}

	prev := make([]byte, 32)
	versionOK := true
	for i := range d.Versions {
		v := &d.Versions[i]
		want := s.recompute(prev, v.TS, v.Author, v.Content)
		if !bytes.Equal(want, v.Hash) {
			versionOK = false
		}
		prev = want
	}
	res.VersionChainOK = versionOK

	prev = make([]byte, 32)
	amendOK := true
	for i := range d.Amends {
		a := &d.Amends[i]
		want := s.recompute(prev, a.TS, a.User, a.Content)
		if !bytes.Equal(want, a.Hash) {
			amendOK = false
		}
		prev = want
	}
	res.AmendChainOK = amendOK

	if d.Sealed {
		last := d.Versions[len(d.Versions)-1].Hash
		var genbuf [8]byte
		binary.BigEndian.PutUint64(genbuf[:], uint64(d.Gen))
		h := sha256.New()
		h.Write(last)
		h.Write(genbuf[:])
		res.SealHash = h.Sum(nil)
		s.BumpHash()
	}
	return res, nil
}

// recompute 重算一个链哈希；Verify 计数为 版本数+补记数+1（封存时额外 1 次）。
func (s *Seal) recompute(prev []byte, ts int64, author string, content []byte) []byte {
	s.BumpHash()
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
