// Package fund 保存每位参保人每个年度的五个累计量，
// 并提供结算推进与冲正恢复所需的快照机制。
package fund

import "errors"

var (
	// ErrInvalidParam 年度等参数非法。
	ErrInvalidParam = errors.New("fund: invalid parameter")
	// ErrYearClosed 请求年度早于该参保人已结算的最大年度。
	ErrYearClosed = errors.New("fund: year closed")
	// ErrPersonUnknown 参保人不存在。
	ErrPersonUnknown = errors.New("fund: unknown person")
)

// Acc 一个（参保人，年度）的五元组累计器。
type Acc struct {
	Du int64 // 已用起付线
	X  int64 // 已进入分段的累计额
	F  int64 // 基本医保基金已付
	P  int64 // 合规自付累计
	Q  int64 // 救助已付
}

// yearState 年度累计器与其未冲正结算计数。
type yearState struct {
	acc       Acc
	openCount int // 当前未冲正结算笔数
}

type person struct {
	years   map[int]*yearState
	maxYear int
}

// Ledger 全体参保人的年度累计账本。并发安全由 settle.Engine 的外层锁保证。
type Ledger struct {
	people map[string]*person
}

// New 创建空账本。
func New() *Ledger {
	return &Ledger{people: make(map[string]*person)}
}

// AddPerson 登记参保人。
func (l *Ledger) AddPerson(name string) error {
	if name == "" {
		return ErrInvalidParam
	}
	if _, ok := l.people[name]; ok {
		return ErrInvalidParam
	}
	l.people[name] = &person{years: make(map[int]*yearState), maxYear: 0}
	return nil
}

// HasPerson 判断参保人是否存在。
func (l *Ledger) HasPerson(name string) bool {
	_, ok := l.people[name]
	return ok
}

// use 定位年度状态；若该参保人尚未结算到该年度且年度合法则新建（从零开始）。
func (p *person) use(year int) (*yearState, error) {
	if year < 1 || year > 9999 {
		return nil, ErrInvalidParam
	}
	if p.maxYear != 0 && year < p.maxYear {
		return nil, ErrYearClosed
	}
	st, ok := p.years[year]
	if !ok {
		st = &yearState{}
		p.years[year] = st
	}
	if year > p.maxYear {
		p.maxYear = year
	}
	return st, nil
}

// Use 返回该人该年度累计器，并校验年度单调。
// 年度合法即建立（新年度从零开始）；调用方结算成功后必须调用 Commit。
func (l *Ledger) Use(name string, year int) (Acc, error) {
	p, ok := l.people[name]
	if !ok {
		return Acc{}, ErrPersonUnknown
	}
	st, err := p.use(year)
	if err != nil {
		return Acc{}, err
	}
	return st.acc, nil
}

// Commit 写入一笔成功结算：按 delta 推进五元组。
// 结算前快照由 settle 包随结算记录自行保存，以支持跨年度的后进先出冲正。
func (l *Ledger) Commit(name string, year int, delta Acc) error {
	p, ok := l.people[name]
	if !ok {
		return ErrPersonUnknown
	}
	st, err := p.use(year)
	if err != nil {
		return err
	}
	st.acc.Du += delta.Du
	st.acc.X += delta.X
	st.acc.F += delta.F
	st.acc.P += delta.P
	st.acc.Q += delta.Q
	st.openCount++
	return nil
}

// Restore 冲正恢复：把该人该年度的累计器置回快照值，
// 并重算 maxYear，使最新年度最后一笔被冲正后该年度可再次结算。
// 调用方须保证 LIFO 次序（由 settle 包的未冲正栈强制）。
func (l *Ledger) Restore(name string, year int, before Acc) (Acc, error) {
	p, ok := l.people[name]
	if !ok {
		return Acc{}, ErrPersonUnknown
	}
	st, ok := p.years[year]
	if !ok || st.openCount <= 0 {
		return Acc{}, ErrInvalidParam
	}
	st.acc = before
	st.openCount--
	// maxYear = 仍有未冲正结算的最大年度；没有则归 0。
	maxY := 0
	for y, ys := range p.years {
		if ys.openCount > 0 && y > maxY {
			maxY = y
		}
	}
	p.maxYear = maxY
	return st.acc, nil
}

// Snapshot 读取某人某年当前累计器，年度尚未建立时返回零值。
func (l *Ledger) Snapshot(name string, year int) (Acc, bool) {
	p, ok := l.people[name]
	if !ok {
		return Acc{}, false
	}
	st, ok := p.years[year]
	if !ok {
		return Acc{}, true
	}
	return st.acc, true
}
