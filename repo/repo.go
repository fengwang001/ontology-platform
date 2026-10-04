// Package repo 在质押库之上实现融资回购、到期购回与违约处置。
package repo

import (
	"sort"

	"ontology/haircut"
	"ontology/pledge"
)

// 回购状态。
const (
	StatusOpen = iota
	StatusRepaid
	StatusDefault
)

// Repo 记录一笔回购的完整生命周期信息。
type Repo struct {
	ID      []byte
	Acct    []byte
	Amount  int64
	Days    int
	RateBps int
	Due     int
	DueAmt  int64
	Status  int
	BadDebt int64
	seq     int64
}

// System 是回购系统，复用质押库的全部持仓与额度能力。
type System struct {
	*pledge.System
	repos map[string]*Repo
}

// New 创建回购系统。
func New() *System {
	s := &System{
		System: pledge.New(),
		repos:  make(map[string]*Repo),
	}
	s.System.SetEnterHook(func(day int) { s.settleLocked(day) })
	return s
}

// Repo 发起融资回购。参数与日期检查通过后先结算到期回购，再做欠库与额度检查。
func (s *System) Repo(day int, id, acct []byte, amount int64, days, r int) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(id) || !haircut.NonEmpty(acct) ||
		!haircut.ValidQty(amount) || days < 1 || days > 365 || !haircut.ValidRateBps(r) {
		return haircut.ErrInvalid
	}
	kID, kAcct := string(id), string(acct)
	s.Begin()
	defer s.Commit()
	if err := s.AdvanceLocked(day); err != nil {
		return err
	}
	// 结算由 pledge.System 的 OnEnter 钩子在日期推进后统一完成。
	if _, ok := s.repos[kID]; ok {
		return haircut.ErrDupRepo
	}
	if !s.AcctExistsLocked(kAcct) {
		return haircut.ErrNoAccount
	}
	if s.IsDeficitLocked(kAcct) {
		return haircut.ErrDeficit
	}
	occ := haircut.Occupy(amount)
	if !s.AddUseLocked(kAcct, occ) {
		return haircut.ErrCap
	}
	interest := haircut.Interest(amount, r, days)
	rp := &Repo{
		ID:      append([]byte(nil), id...),
		Acct:    append([]byte(nil), acct...),
		Amount:  amount,
		Days:    days,
		RateBps: r,
		Due:     day + days,
		DueAmt:  amount + interest,
		Status:  StatusOpen,
		seq:     s.NextSeqLocked(),
	}
	s.repos[kID] = rp
	s.AddCashLocked(kAcct, amount)
	return nil
}

// settleLocked 结算所有到期日不晚于 day 的未了结回购，按 (到期日, 接受序号) 序。
func (s *System) settleLocked(day int) {
	due := make([]*Repo, 0)
	for _, rp := range s.repos {
		if rp.Status == StatusOpen && rp.Due <= day {
			due = append(due, rp)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].Due != due[j].Due {
			return due[i].Due < due[j].Due
		}
		return due[i].seq < due[j].seq
	})
	for _, rp := range due {
		s.settleOneLocked(rp)
	}
}

// settleOneLocked 结算单笔：现金足额则扣款购回，否则不扣现金、处置在库券。
func (s *System) settleOneLocked(rp *Repo) {
	acct := string(rp.Acct)
	occ := haircut.Occupy(rp.Amount)
	if s.CashLocked(acct) >= rp.DueAmt {
		s.PayCashLocked(acct, rp.DueAmt)
		s.ReleaseUseLocked(acct, occ)
		rp.Status = StatusRepaid
		return
	}
	rp.Status = StatusDefault
	owed := rp.DueAmt
	bonds := s.PledgedSortedLocked(acct)
	for _, bond := range bonds {
		if owed <= 0 {
			break
		}
		price := s.BondPrice(bond)
		need := haircut.CeilDiv(owed, price)
		_, proceeds := s.SellPledgedLocked(acct, bond, need)
		if proceeds >= owed {
			s.AddCashLocked(acct, proceeds-owed)
			owed = 0
			break
		}
		owed -= proceeds
	}
	if owed > 0 {
		rp.BadDebt = owed
	}
	s.ReleaseUseLocked(acct, occ)
}

// GetRepo 返回回购副本，不存在返回 nil。
func (s *System) GetRepo(id []byte) *Repo {
	s.Begin()
	defer s.Commit()
	rp := s.repos[string(id)]
	if rp == nil {
		return nil
	}
	cp := *rp
	cp.ID = append([]byte(nil), rp.ID...)
	cp.Acct = append([]byte(nil), rp.Acct...)
	return &cp
}

// RepoCount 返回已接受回购总数。
func (s *System) RepoCount() int {
	s.Begin()
	defer s.Commit()
	return len(s.repos)
}

// RepoStatus 返回到期状态（StatusOpen/StatusRepaid/StatusDefault），不存在返回 -1。
func (s *System) RepoStatus(id []byte) int {
	s.Begin()
	defer s.Commit()
	rp := s.repos[string(id)]
	if rp == nil {
		return -1
	}
	return rp.Status
}
