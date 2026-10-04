// Package loan 维护融券券源总账：出借人登记、空闲量与借券合约。
package loan

// ID 是出借人/借入人/标的等非空字节串在包内的不可变键形式。
type ID string

// Kind 区分普通合约与召回合约。
type Kind uint8

const (
	// Ordinary 为未被召回的普通合约。
	Ordinary Kind = iota
	// Recalled 为撤回时拆出的召回合约。
	Recalled
)

// Contract 是一笔（出借人，借入人，标的，量）合约；Recalled 时 Dl 为截止时刻。
type Contract struct {
	ID       int64
	Lender   ID
	Borrower ID
	Symbol   ID
	Qty      int64
	Kind     Kind
	Dl       int64
}

// Book 是全部标的的券源总账。
type Book struct {
	syms      map[ID]*symBook
	contracts map[int64]*Contract
}

type lenderState struct {
	regSeq        int64
	idle          int64
	ordinaryTotal int64
	ordinaryIDs   []int64 // 合约号升序
}

type borrowerState struct {
	recallIDs   []int64 // (dl, 合约号) 升序
	ordinaryIDs []int64 // 合约号升序
}

type symBook struct {
	totalLend   int64
	idleTotal   int64
	activeTotal int64
	handedBack  int64
	nextReg     int64
	lenders     map[ID]*lenderState
	borrowers   map[ID]*borrowerState
}

// NewBook 创建空总账。
func NewBook() *Book {
	return &Book{
		syms:      make(map[ID]*symBook),
		contracts: make(map[int64]*Contract),
	}
}

func (b *Book) sym(sym ID) *symBook {
	s := b.syms[sym]
	if s == nil {
		s = &symBook{lenders: make(map[ID]*lenderState), borrowers: make(map[ID]*borrowerState)}
		b.syms[sym] = s
	}
	return s
}

func lenderOf(s *symBook, lender ID) *lenderState {
	l := s.lenders[lender]
	if l == nil {
		l = &lenderState{}
		s.lenders[lender] = l
	}
	return l
}

func borrowerOf(s *symBook, borrower ID) *borrowerState {
	br := s.borrowers[borrower]
	if br == nil {
		br = &borrowerState{}
		s.borrowers[borrower] = br
	}
	return br
}

func (b *Book) put(c *Contract) {
	b.contracts[c.ID] = c
	s := b.sym(c.Symbol)
	s.activeTotal += c.Qty
	l := lenderOf(s, c.Lender)
	br := borrowerOf(s, c.Borrower)
	if c.Kind == Ordinary {
		l.ordinaryTotal += c.Qty
		l.ordinaryIDs = append(l.ordinaryIDs, c.ID) // 合约号严格递增
		br.ordinaryIDs = append(br.ordinaryIDs, c.ID)
		return
	}
	pos := len(br.recallIDs)
	for i := len(br.recallIDs) - 1; i >= 0; i-- {
		old := b.contracts[br.recallIDs[i]]
		if old == nil || old.Dl <= c.Dl {
			break
		}
		pos = i
	}
	br.recallIDs = insertAt(br.recallIDs, pos, c.ID)
}

func insertAt(ids []int64, pos int, id int64) []int64 {
	ids = append(ids, 0)
	copy(ids[pos+1:], ids[pos:])
	ids[pos] = id
	return ids
}

// AddLend 增加出借人空闲量；首次出借返回其新登记序号，否则返回 0。
func (b *Book) AddLend(sym, lender ID, qty int64) (regSeq int64) {
	s := b.sym(sym)
	l := lenderOf(s, lender)
	if l.regSeq == 0 {
		s.nextReg++
		l.regSeq = s.nextReg
		regSeq = l.regSeq
	}
	l.idle += qty
	s.idleTotal += qty
	s.totalLend += qty
	return regSeq
}

// Idle 返回某出借人在某标的的空闲量。
func (b *Book) Idle(sym, lender ID) int64 {
	if s := b.syms[sym]; s != nil {
		if l := s.lenders[lender]; l != nil {
			return l.idle
		}
	}
	return 0
}

// IdleTotal 返回某标的全池空闲量之和。
func (b *Book) IdleTotal(sym ID) int64 {
	if s := b.syms[sym]; s != nil {
		return s.idleTotal
	}
	return 0
}

// HasReg 报告出借人在该标的是否已登记。
func (b *Book) HasReg(sym, lender ID) bool {
	if s := b.syms[sym]; s != nil {
		if l := s.lenders[lender]; l != nil {
			return l.regSeq != 0
		}
	}
	return false
}

// RegCount 返回某标的已登记出借人数量。
func (b *Book) RegCount(sym ID) int {
	if s := b.syms[sym]; s != nil {
		return len(s.lenders)
	}
	return 0
}

// RegSeq 返回出借人登记序号；未登记为 0。
func (b *Book) RegSeq(sym, lender ID) int64 {
	if s := b.syms[sym]; s != nil {
		if l := s.lenders[lender]; l != nil {
			return l.regSeq
		}
	}
	return 0
}

// Lenders 返回某标的已登记出借人键。
func (b *Book) Lenders(sym ID) []ID {
	s := b.syms[sym]
	if s == nil {
		return nil
	}
	out := make([]ID, 0, len(s.lenders))
	for k := range s.lenders {
		out = append(out, k)
	}
	return out
}

// LenderOrdinaryTotal 返回出借人名下普通（未召回）合约量之和。
func (b *Book) LenderOrdinaryTotal(sym, lender ID) int64 {
	if s := b.syms[sym]; s != nil {
		if l := s.lenders[lender]; l != nil {
			return l.ordinaryTotal
		}
	}
	return 0
}

// Debt 返回借入人在某标的的欠券总量。
func (b *Book) Debt(sym, borrower ID) int64 {
	s := b.syms[sym]
	if s == nil {
		return 0
	}
	br := s.borrowers[borrower]
	if br == nil {
		return 0
	}
	var total int64
	for _, id := range br.recallIDs {
		total += b.contracts[id].Qty
	}
	for _, id := range br.ordinaryIDs {
		total += b.contracts[id].Qty
	}
	return total
}

// BorrowerHasDebt 报告借入人在该标的是否有欠券。
func (b *Book) BorrowerHasDebt(sym, borrower ID) bool {
	return b.Debt(sym, borrower) > 0
}

// TakeIdle 从出借人空闲量中取走 qty。
func (b *Book) TakeIdle(sym, lender ID, qty int64) {
	s := b.sym(sym)
	l := s.lenders[lender]
	l.idle -= qty
	s.idleTotal -= qty
}

// AddIdle 向出借人空闲量加回 qty（普通合约归还）。
func (b *Book) AddIdle(sym, lender ID, qty int64) {
	s := b.sym(sym)
	l := lenderOf(s, lender)
	l.idle += qty
	s.idleTotal += qty
}

// NewOrdinary 创建一笔普通合约。
func (b *Book) NewOrdinary(id int64, sym, lender, borrower ID, qty int64) {
	b.put(&Contract{ID: id, Lender: lender, Borrower: borrower, Symbol: sym, Qty: qty, Kind: Ordinary})
}

// LenderOrdinaryIDs 返回出借人名下普通合约号（升序）。
func (b *Book) LenderOrdinaryIDs(sym, lender ID) []int64 {
	if s := b.syms[sym]; s != nil {
		l := s.lenders[lender]
		if l == nil {
			return nil
		}
		return append([]int64(nil), l.ordinaryIDs...)
	}
	return nil
}

// SplitReplacement 从原普通合约扣减 qty，另建接手人的普通合约。
func (b *Book) SplitReplacement(newID int64, origID int64, replacement ID, qty int64) {
	orig := b.contracts[origID]
	b.reduceLocked(orig, qty)
	b.put(&Contract{
		ID:       newID,
		Lender:   replacement,
		Borrower: orig.Borrower,
		Symbol:   orig.Symbol,
		Qty:      qty,
		Kind:     Ordinary,
	})
}

// SplitRecall 从原普通合约扣减 qty，拆出召回合约。
func (b *Book) SplitRecall(newID, dl int64, origID int64, qty int64) {
	orig := b.contracts[origID]
	b.reduceLocked(orig, qty)
	b.put(&Contract{
		ID:       newID,
		Lender:   orig.Lender,
		Borrower: orig.Borrower,
		Symbol:   orig.Symbol,
		Qty:      qty,
		Kind:     Recalled,
		Dl:       dl,
	})
}

// ReturnRecall 从召回合约扣减 qty 并直接交还出借人。
func (b *Book) ReturnRecall(id, qty int64) { b.Reduce(id, qty) }

// ReturnOrdinary 从普通合约扣减 qty 并回到出借人空闲量。
func (b *Book) ReturnOrdinary(id, qty int64) {
	c := b.contracts[id]
	b.AddIdle(c.Symbol, c.Lender, qty)
	b.Reduce(id, qty)
}

// BorrowerRecallIDs 返回借入人名下召回合约号（按 dl、合约号升序）。
func (b *Book) BorrowerRecallIDs(sym, borrower ID) []int64 {
	if s := b.syms[sym]; s != nil {
		br := s.borrowers[borrower]
		if br == nil {
			return nil
		}
		return append([]int64(nil), br.recallIDs...)
	}
	return nil
}

// BorrowerOrdinaryIDs 返回借入人名下普通合约号（升序）。
func (b *Book) BorrowerOrdinaryIDs(sym, borrower ID) []int64 {
	if s := b.syms[sym]; s != nil {
		br := s.borrowers[borrower]
		if br == nil {
			return nil
		}
		return append([]int64(nil), br.ordinaryIDs...)
	}
	return nil
}

func removeID(ids []int64, id int64) []int64 {
	for i, v := range ids {
		if v == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

// Reduce 扣减合约量；清零即了结。
func (b *Book) Reduce(id, qty int64) {
	if c := b.contracts[id]; c != nil {
		b.reduceLocked(c, qty)
	}
}

func (b *Book) reduceLocked(c *Contract, qty int64) {
	c.Qty -= qty
	s := b.sym(c.Symbol)
	s.activeTotal -= qty
	l := s.lenders[c.Lender]
	br := s.borrowers[c.Borrower]
	if c.Kind == Ordinary {
		l.ordinaryTotal -= qty
	}
	if c.Qty > 0 {
		return
	}
	delete(b.contracts, c.ID)
	if c.Kind == Ordinary {
		l.ordinaryIDs = removeID(l.ordinaryIDs, c.ID)
		br.ordinaryIDs = removeID(br.ordinaryIDs, c.ID)
	} else {
		br.recallIDs = removeID(br.recallIDs, c.ID)
	}
}

// AddHandedBack 增加某标的累计交还量。
func (b *Book) AddHandedBack(sym ID, qty int64) { b.sym(sym).handedBack += qty }

// Get 按合约号取合约。
func (b *Book) Get(id int64) (Contract, bool) {
	if c := b.contracts[id]; c != nil {
		return *c, true
	}
	return Contract{}, false
}

// TotalLend 返回某标的累计 Lend 量。
func (b *Book) TotalLend(sym ID) int64 {
	if s := b.syms[sym]; s != nil {
		return s.totalLend
	}
	return 0
}

// ActiveTotal 返回某标的未了结合约量之和。
func (b *Book) ActiveTotal(sym ID) int64 {
	if s := b.syms[sym]; s != nil {
		return s.activeTotal
	}
	return 0
}

// HandedBack 返回某标的累计交还量。
func (b *Book) HandedBack(sym ID) int64 {
	if s := b.syms[sym]; s != nil {
		return s.handedBack
	}
	return 0
}

// Symbols 返回当前所有标的键。
func (b *Book) Symbols() []ID {
	out := make([]ID, 0, len(b.syms))
	for k := range b.syms {
		out = append(out, k)
	}
	return out
}
