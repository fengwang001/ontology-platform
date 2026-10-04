// Package pledge 管理账户可用持仓、质押库存与增量标准券额度。
package pledge

import (
	"sort"
	"sync"

	"ontology/haircut"
)

// Account 是一个资金账户的质押视图。
type Account struct {
	Cash    int64
	Avail   map[string]int64
	Pledged map[string]int64
	Cap     int64
	Use     int64
}

// System 是质押库本体，repo 包在其上实现回购。所有变更以全局互斥串行化。
type System struct {
	mu      sync.RWMutex
	mk      *haircut.Market
	day     int
	accts   map[string]*Account
	seq     int64
	touched int
	OnEnter func(day int)
}

// New 创建质押库。
func New() *System {
	return &System{
		mk:    haircut.NewMarket(),
		accts: make(map[string]*Account),
	}
}

// SetEnterHook 安装入口结算钩子（repo 包安装），须在任何业务调用之前设置一次。
func (s *System) SetEnterHook(h func(day int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OnEnter = h
}

func (s *System) hasBond(bond string) bool {
	_, ok := s.mk.Get(bond)
	return ok
}

// BondRate 返回债券当前折算率。
func (s *System) BondRate(bond string) int {
	b, _ := s.mk.Get(bond)
	return b.Rate
}

// BondPrice 返回债券当前净价。
func (s *System) BondPrice(bond string) int64 {
	b, _ := s.mk.Get(bond)
	return b.Price
}

func (s *System) getAcct(acct string) *Account { return s.accts[acct] }

// Acct 返回账户视图副本，不存在返回 nil。
func (s *System) Acct(acct []byte) *Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := s.accts[string(acct)]
	if a == nil {
		return nil
	}
	return &Account{
		Cash:    a.Cash,
		Cap:     a.Cap,
		Use:     a.Use,
		Avail:   copyMap(a.Avail),
		Pledged: copyMap(a.Pledged),
	}
}

func copyMap(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Day 返回当前日期。
func (s *System) Day() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.day
}

// ---- 写锁内原语（调用方持锁）----

func (s *System) nextSeqLocked() int64 {
	s.seq++
	return s.seq
}

func (s *System) advanceLocked(day int) error {
	if day < s.day {
		return haircut.ErrDayBackward
	}
	s.day = day
	if s.OnEnter != nil {
		s.OnEnter(day)
	}
	return nil
}

// setRateLocked 增量更新持有该券账户的 Cap；touched 等于持有账户数。
func (s *System) setRateLocked(bond string, rate int) error {
	old := s.BondRate(bond)
	if err := s.mk.SetRate(bond, rate); err != nil {
		return err
	}
	s.touched = 0
	for _, a := range s.accts {
		if n := a.Pledged[bond]; n > 0 {
			a.Cap += haircut.StdBond(n, rate) - haircut.StdBond(n, old)
			s.touched++
		}
	}
	return nil
}

func (s *System) setPriceLocked(bond string, price int64) error {
	s.touched = 0
	return s.mk.SetPrice(bond, price)
}

func (s *System) creditLocked(acct, bond string, n int64) {
	a := s.accts[acct]
	if a == nil {
		a = &Account{Avail: map[string]int64{}, Pledged: map[string]int64{}}
		s.accts[acct] = a
	}
	a.Avail[bond] += n
}

func (s *System) creditCashLocked(acct string, amt int64) {
	a := s.accts[acct]
	if a == nil {
		a = &Account{Avail: map[string]int64{}, Pledged: map[string]int64{}}
		s.accts[acct] = a
	}
	a.Cash += amt
}

// pledgeInLocked 可用转在库，按当前折算率增量增加 Cap。
func (s *System) pledgeInLocked(acct, bond string, n int64) error {
	a := s.accts[acct]
	if a.Avail[bond] < n {
		return haircut.ErrAvail
	}
	a.Avail[bond] -= n
	oldN := a.Pledged[bond]
	newN := oldN + n
	a.Pledged[bond] = newN
	rate := s.BondRate(bond)
	a.Cap += haircut.StdBond(newN, rate) - haircut.StdBond(oldN, rate)
	return nil
}

// pledgeOutLocked 先核库存，再要求出库后 Cap>=Use；touched 恰好为 1。
func (s *System) pledgeOutLocked(acct, bond string, n int64) error {
	a := s.accts[acct]
	s.touched = 0
	if a.Pledged[bond] < n {
		return haircut.ErrStock
	}
	s.touched++
	rate := s.BondRate(bond)
	newCap := a.Cap -
		(haircut.StdBond(a.Pledged[bond], rate) - haircut.StdBond(a.Pledged[bond]-n, rate))
	if newCap < a.Use {
		return haircut.ErrCap
	}
	a.Pledged[bond] -= n
	a.Avail[bond] += n
	a.Cap = newCap
	return nil
}

// AddUseLocked 增加占用，Cap 不足返回 false；额度检查 touched 恒为 0。
func (s *System) AddUseLocked(acct string, occ int64) bool {
	a := s.accts[acct]
	s.touched = 0
	if a.Cap < a.Use+occ {
		return false
	}
	a.Use += occ
	return true
}

// ReleaseUseLocked 释放一笔已了结回购的占用。
func (s *System) ReleaseUseLocked(acct string, occ int64) {
	s.accts[acct].Use -= occ
}

// CashLocked 读取现金。
func (s *System) CashLocked(acct string) int64 { return s.accts[acct].Cash }

// PayCashLocked 扣减现金。
func (s *System) PayCashLocked(acct string, amt int64) { s.accts[acct].Cash -= amt }

// AddCashLocked 增加现金。
func (s *System) AddCashLocked(acct string, amt int64) { s.accts[acct].Cash += amt }

// SellPledgedLocked 违约处置：卖出某券至多 n 张，返回卖出张数与得款。
func (s *System) SellPledgedLocked(acct, bond string, n int64) (sold, proceeds int64) {
	a := s.accts[acct]
	sold = a.Pledged[bond]
	if sold > n {
		sold = n
	}
	oldN := a.Pledged[bond]
	newN := oldN - sold
	a.Pledged[bond] = newN
	if newN == 0 {
		delete(a.Pledged, bond)
	}
	rate := s.BondRate(bond)
	a.Cap -= haircut.StdBond(oldN, rate) - haircut.StdBond(newN, rate)
	return sold, sold * s.BondPrice(bond)
}

// PledgedSortedLocked 按债券字节序列出账户在库券种。
func (s *System) PledgedSortedLocked(acct string) []string {
	a := s.accts[acct]
	out := make([]string, 0, len(a.Pledged))
	for b, n := range a.Pledged {
		if n > 0 {
			out = append(out, b)
		}
	}
	sort.Strings(out)
	return out
}

func (s *System) deficitLocked(acct string) bool {
	a := s.accts[acct]
	return a.Cap < a.Use
}

// IsDeficitLocked 返回账户是否欠库（调用方持写锁）。
func (s *System) IsDeficitLocked(acct string) bool { return s.deficitLocked(acct) }

// AcctExistsLocked 判断账户是否存在（调用方持锁）。
func (s *System) AcctExistsLocked(acct string) bool { return s.accts[acct] != nil }

// Begin 获取写锁并重置 touched。
func (s *System) Begin() {
	s.mu.Lock()
	s.touched = 0
}

// Commit 释放写锁。
func (s *System) Commit() { s.mu.Unlock() }

// AdvanceLocked 在写锁内推进日期。
func (s *System) AdvanceLocked(day int) error { return s.advanceLocked(day) }

// NextSeqLocked 在写锁内分配被接受序号。
func (s *System) NextSeqLocked() int64 { return s.nextSeqLocked() }

// TouchedLocked 返回本操作至今触碰的在库债券记录数。
func (s *System) TouchedLocked() int { return s.touched }

// AddBond 登记债券：rate 0..150，price 1..1e6，重复报 ErrDupBond。
func (s *System) AddBond(day int, bond []byte, rate int, price int64) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(bond) ||
		!haircut.ValidRate(rate) || !haircut.ValidPrice(price) {
		return haircut.ErrInvalid
	}
	b := string(bond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(day); err != nil {
		return err
	}
	return s.mk.Add(b, rate, price)
}

// SetRate 调整折算率；touched 为在库持有该券的账户数，可能造成欠库。
func (s *System) SetRate(day int, bond []byte, rate int) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(bond) || !haircut.ValidRate(rate) {
		return haircut.ErrInvalid
	}
	b := string(bond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(day); err != nil {
		return err
	}
	if !s.hasBond(b) {
		return haircut.ErrNoBond
	}
	return s.setRateLocked(b, rate)
}

// SetPrice 调整净价，不影响额度。
func (s *System) SetPrice(day int, bond []byte, price int64) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(bond) || !haircut.ValidPrice(price) {
		return haircut.ErrInvalid
	}
	b := string(bond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(day); err != nil {
		return err
	}
	if !s.hasBond(b) {
		return haircut.ErrNoBond
	}
	return s.setPriceLocked(b, price)
}

// Credit 增加可用持仓，账户不存在则建立。
func (s *System) Credit(day int, acct, bond []byte, n int64) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(acct) ||
		!haircut.NonEmpty(bond) || !haircut.ValidQty(n) {
		return haircut.ErrInvalid
	}
	a, b := string(acct), string(bond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(day); err != nil {
		return err
	}
	if !s.hasBond(b) {
		return haircut.ErrNoBond
	}
	s.creditLocked(a, b, n)
	return nil
}

// CreditCash 增加现金，账户不存在则建立。
func (s *System) CreditCash(day int, acct []byte, amt int64) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(acct) || !haircut.ValidQty(amt) {
		return haircut.ErrInvalid
	}
	a := string(acct)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(day); err != nil {
		return err
	}
	s.creditCashLocked(a, amt)
	return nil
}

// PledgeIn 可用转在库，欠库时不禁止。
func (s *System) PledgeIn(day int, acct, bond []byte, n int64) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(acct) ||
		!haircut.NonEmpty(bond) || !haircut.ValidQty(n) {
		return haircut.ErrInvalid
	}
	a, b := string(acct), string(bond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(day); err != nil {
		return err
	}
	if !s.hasBond(b) {
		return haircut.ErrNoBond
	}
	if s.getAcct(a) == nil {
		return haircut.ErrNoAccount
	}
	s.touched = 0
	return s.pledgeInLocked(a, b, n)
}

// PledgeOut 出库；欠库报 ErrDeficit，库存不足报 ErrStock，出库后额度不足报 ErrCap。
func (s *System) PledgeOut(day int, acct, bond []byte, n int64) error {
	if !haircut.ValidDay(day) || !haircut.NonEmpty(acct) ||
		!haircut.NonEmpty(bond) || !haircut.ValidQty(n) {
		return haircut.ErrInvalid
	}
	a, b := string(acct), string(bond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(day); err != nil {
		return err
	}
	if !s.hasBond(b) {
		return haircut.ErrNoBond
	}
	if s.getAcct(a) == nil {
		return haircut.ErrNoAccount
	}
	if s.deficitLocked(a) {
		return haircut.ErrDeficit
	}
	return s.pledgeOutLocked(a, b, n)
}

// Deficit 描述一个欠库账户与缺口。
type Deficit struct {
	Acct    []byte
	Missing int64
}

// Deficits 按账户字节序列出欠库账户与缺口 Use-Cap。
func (s *System) Deficits() []Deficit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.accts))
	for k, a := range s.accts {
		if a.Cap < a.Use {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]Deficit, 0, len(keys))
	for _, k := range keys {
		a := s.accts[k]
		out = append(out, Deficit{Acct: []byte(k), Missing: a.Use - a.Cap})
	}
	return out
}
