// Package seal 提供自动/手动封存、限时解封、封存后补记与校验。
package seal

import "ontology/record"

const maxTS int64 = 1_000_000_000

func validNow(now int64, a, b string) bool {
	return now >= 0 && now <= maxTS && a != "" && b != ""
}

// Seal：归档员手动封存，要求已审签且当前不处于解封窗口。
func Seal(s *record.Store, now int64, user, doc string) error {
	if !validNow(now, user, doc) {
		return record.ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	u, d, snap, h0, err := s.BeginTx(now, user, doc)
	if err != nil {
		return err
	}
	if !u.Roles[record.RoleArchivist] {
		s.Rollback(snap, h0)
		return record.ErrPermission
	}
	s.Sweep(now)
	if s.EffectiveSealed(d, now) || d.WindowOpen || d.Status != record.StatusCosigned {
		s.Rollback(snap, h0)
		return record.ErrState
	}
	s.ApplySeal(d, now)
	s.Commit(now)
	return nil
}

// Amend：仅封存后可做；作者本人或同科室 level>=2。
func Amend(s *record.Store, now int64, user, doc, content string) error {
	if !validNow(now, user, doc) {
		return record.ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	u, d, snap, h0, err := s.BeginTx(now, user, doc)
	if err != nil {
		return err
	}
	author := s.GetUser(d.Author)
	priv := u.Name == d.Author || (author != nil && u.Dept == author.Dept && u.Level >= 2)
	if !priv {
		s.Rollback(snap, h0)
		return record.ErrPermission
	}
	s.Sweep(now)
	if !s.EffectiveSealed(d, now) {
		s.Rollback(snap, h0)
		return record.ErrState
	}
	s.AppendAmendment(d, now, user, content)
	s.Commit(now)
	return nil
}

// Unseal：a 有医务审批、b 有病案审批、两人不同；仅有效封存中的文档。
func Unseal(s *record.Store, now int64, a, b, doc string) error {
	if !validNow(now, a, b) || doc == "" {
		return record.ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	users, d, snap, h0, err := s.BeginTxMulti(now, doc, a, b)
	if err != nil {
		return err
	}
	ua, ub := users[a], users[b]
	if ua.Name == ub.Name || !ua.Roles[record.RoleMedical] || !ub.Roles[record.RoleRecords] {
		s.Rollback(snap, h0)
		return record.ErrPermission
	}
	s.Sweep(now)
	if !s.EffectiveSealed(d, now) {
		s.Rollback(snap, h0)
		return record.ErrState
	}
	s.OpenUnsealWindow(d, now)
	s.Commit(now)
	return nil
}

// Verify 重算版本链、补记链并返回当前 sealHash。
func Verify(s *record.Store, doc string) (record.VerifyResult, error) {
	if doc == "" {
		return record.VerifyResult{}, record.ErrInvalid
	}
	return s.Verify(doc)
}
