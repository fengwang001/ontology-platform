// Package sign 提供文档签名、上级审签与退回。
package sign

import "ontology/record"

// Sign：仅作者；草稿转已签；作者 level>=2 直接成为已审签。封存后可迟签。
func Sign(s *record.Store, now int64, user, doc string) error {
	if now < 0 || now > 1_000_000_000 || user == "" || doc == "" {
		return record.ErrInvalid
	}
	return s.RunOp(now, user, doc, func(u *record.User, d *record.Doc) error {
		if u.Name != d.Author {
			return record.ErrPermission
		}
		if d.Status != record.StatusDraft {
			return record.ErrState
		}
		sealed := s.EffectiveSealed(d, now)
		s.MarkSigned(d, u, now, sealed)
		return nil
	})
}

// Cosign：要求已签；同科室、level>=2、非作者本人。封存后可迟签。
func Cosign(s *record.Store, now int64, user, doc string) error {
	if now < 0 || now > 1_000_000_000 || user == "" || doc == "" {
		return record.ErrInvalid
	}
	return s.RunOp(now, user, doc, func(u *record.User, d *record.Doc) error {
		author := s.GetUser(d.Author)
		if u.Name == d.Author || author == nil || u.Dept != author.Dept || u.Level < 2 {
			return record.ErrPermission
		}
		if d.Status != record.StatusSigned {
			return record.ErrState
		}
		s.MarkCosigned(d, u, now, s.EffectiveSealed(d, now))
		return nil
	})
}

// Return：权限同 Cosign；已签退回草稿。封存后报状态不符。
func Return(s *record.Store, now int64, user, doc string) error {
	if now < 0 || now > 1_000_000_000 || user == "" || doc == "" {
		return record.ErrInvalid
	}
	return s.RunOp(now, user, doc, func(u *record.User, d *record.Doc) error {
		author := s.GetUser(d.Author)
		if u.Name == d.Author || author == nil || u.Dept != author.Dept || u.Level < 2 {
			return record.ErrPermission
		}
		if s.EffectiveSealed(d, now) || d.Status != record.StatusSigned {
			return record.ErrState
		}
		s.ResetSignatures(d)
		return nil
	})
}
