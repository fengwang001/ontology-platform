// Package sign 提供签名、上级审签与退回。
package sign

import "ontology/record"

// Sign 服务。
type Sign struct {
	*record.Record
}

// New 创建签名服务：T 为出院后自动封存时限，U 为解封窗口。
func New(T, U int64) *Sign {
	return &Sign{Record: record.New(T, U)}
}

// Wrap 在已有 record 存储上组装签名服务，使三个包共享同一状态与锁。
func Wrap(r *record.Record) *Sign { return &Sign{Record: r} }

// Sign 由作者签名：草稿转已签；作者 level>=2 时直接成为已审签。封存后允许迟签。
func (s *Sign) Sign(now int64, user, doc string) error {
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
	if user != d.Author {
		return record.ErrPerm
	}
	// 先落地到期封存（可能使本次签名成为迟签）。
	s.Commit(s.Plan(now))
	if d.State != record.StateDraft {
		return record.ErrState
	}
	d.AuthorSigned = true
	late := record.EffectiveSealed(d)
	if u.Level >= 2 {
		d.State = record.StateApproved
		if late {
			d.Defect = false
		}
	} else {
		d.State = record.StateSigned
	}
	if late {
		d.LateSign = append(d.LateSign, record.Signature{TS: now, User: user, Late: true})
	}
	s.Advance(now)
	return nil
}

// Cosign 上级审签：须已签，审签人与作者同科室、level>=2、非作者本人。封存后允许迟签。
func (s *Sign) Cosign(now int64, user, doc string) error {
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
	if author == nil || user == d.Author || u.Dept != author.Dept || u.Level < 2 {
		return record.ErrPerm
	}
	s.Commit(s.Plan(now))
	if d.State != record.StateSigned {
		return record.ErrState
	}
	d.State = record.StateApproved
	late := record.EffectiveSealed(d)
	if late {
		d.Defect = false
		d.LateSign = append(d.LateSign, record.Signature{TS: now, User: user, Late: true, Cosign: true})
	}
	s.Advance(now)
	return nil
}

// Return 退回：权限同 Cosign，已签退回草稿；封存后报状态不符。
func (s *Sign) Return(now int64, user, doc string) error {
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
	if author == nil || user == d.Author || u.Dept != author.Dept || u.Level < 2 {
		return record.ErrPerm
	}
	s.Commit(s.Plan(now))
	if record.EffectiveSealed(d) || d.State != record.StateSigned {
		return record.ErrState
	}
	d.State = record.StateDraft
	d.AuthorSigned = false
	s.Advance(now)
	return nil
}
