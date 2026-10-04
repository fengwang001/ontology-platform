// Package pool 实现融券券源池、撤回替换、召回与到期强制买入。
package pool

import (
	"errors"
	"sort"
	"sync"

	"ontology/loan"
	"ontology/recall"
)

const (
	maxNow  = int64(1_000_000_000_000)
	maxQty  = int64(1_000_000_000)
	maxN    = int64(1_000_000)
	maxPen  = int64(10_000)
	penBase = int64(10_000)
)

var (
	ErrInvalid  = errors.New("invalid argument")
	ErrClock    = errors.New("clock moved backwards")
	ErrNotFound = errors.New("not found")
	ErrOverQty  = errors.New("quantity exceeds available")
	ErrNoSupply = errors.New("insufficient securities supply")
)

// LendReceipt 是 Lend 的结果。
type LendReceipt struct {
	Registered bool  // 出借人是否首次在该标的登记
	RegSeq     int64 // 登记序号（首次为新值，否则为既有值）
	Buys       []recall.Buy
}

// BorrowReceipt 是 Borrow 的结果。
type BorrowReceipt struct {
	Contracts []loan.Contract
	Buys      []recall.Buy
}

// WithdrawReceipt 是 Withdraw 的结果。
type WithdrawReceipt struct {
	Replacements []loan.Contract // 替换接手生成的新合约，按生成序
	Recalls      []recall.CContract
	Buys         []recall.Buy
}

// ReturnReceipt 是 Return 的结果。
type ReturnReceipt struct {
	ToRecalled int64 // 还到召回合约、直接交还出借人的量
	ToNormal   int64 // 还到普通合约、回到空闲的量
	Buys       []recall.Buy
}

type normalEntry struct {
	id  int64
	qty int64
}

// symState 是单个标的的全部状态。
type symState struct {
	price     int64
	hasPrice  bool
	totalLend int64 // 累计 Lend 量
	returned  int64 // 累计交还出借人（离开券池）量
	totalFree int64 // 全池空闲量之和

	regSeq  map[string]int64
	nextReg int64

	free map[string]int64

	normalIDs  []int64
	normal     map[int64]*loan.Contract
	normalQty  map[int64]int64
	lenderNorm map[string][]int64

	borrowerDebt map[string]int64
}

func newSymState() *symState {
	return &symState{
		regSeq:       make(map[string]int64),
		free:         make(map[string]int64),
		normal:       make(map[int64]*loan.Contract),
		normalQty:    make(map[int64]int64),
		lenderNorm:   make(map[string][]int64),
		borrowerDebt: make(map[string]int64),
	}
}

// Pool 是券源池。零值不可用，须经 New 创建。
type Pool struct {
	mu     sync.Mutex
	n      int64
	pen    int64
	now    int64
	nextID int64
	syms   map[string]*symState
	book   *recall.Book
	buyLog []recall.Buy

	scanned int64
}

func New(N, pen int64) *Pool { return &Pool{} }

func NewPool(N, pen int64) (*Pool, error) {
	if N < 1 || N > maxN || pen < 0 || pen > maxPen {
		return nil, ErrInvalid
	}
	return &Pool{
		n:    N,
		pen:  pen,
		syms: make(map[string]*symState),
		book: recall.NewBook(),
	}, nil
}

func nonEmpty(b []byte) bool { return len(b) > 0 }

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

func validQty(q int64) bool { return q >= 1 && q <= maxQty }

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

func (p *Pool) sym(sym string) *symState {
	st := p.syms[sym]
	if st == nil {
		st = newSymState()
		p.syms[sym] = st
	}
	return st
}

// settle 推进时钟并把 dl<=now 的召回合约强制买入。
// 每个标的在到期前必有价格（Borrow 前提是已 SetPrice）。
func (p *Pool) settle(now int64) []recall.Buy {
	p.now = now
	due := p.book.Due(now)
	if len(due) == 0 {
		return nil
	}
	buys := make([]recall.Buy, 0, len(due))
	for _, rc := range due {
		r := rc.Qty
		price := p.syms[string(rc.Sym)].price
		base := r * price
		pen := (base*p.pen + penBase - 1) / penBase
		buys = append(buys, recall.Buy{
			ID:     rc.ID,
			Qty:    r,
			Price:  price,
			Pen:    pen,
			Amount: base + pen,
			Time:   rc.DL,
		})
		st := p.syms[string(rc.Sym)]
		st.returned += r
		st.borrowerDebt[string(rc.Borrower)] -= r
		p.book.Remove(rc.ID)
	}
	p.buyLog = append(p.buyLog, buys...)
	return buys
}

func down(h []int64, i, n int) {
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		j := l
		if r := l + 1; r < n && h[r] < h[l] {
			j = r
		}
		if h[i] <= h[j] {
			return
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
}

func (p *Pool) newContractID() int64 {
	p.nextID++
	return p.nextID
}

// removeNormal 删除普通合约索引。
func (st *symState) removeNormal(id int64) {
	c := st.normal[id]
	if c == nil {
		return
	}
	delete(st.normal, id)
	delete(st.normalQty, id)
	ids := st.lenderNorm[string(c.Lender)]
	for i, v := range ids {
		if v == id {
			st.lenderNorm[string(c.Lender)] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	for i, v := range st.normalIDs {
		if v == id {
			st.normalIDs = append(st.normalIDs[:i], st.normalIDs[i+1:]...)
			break
		}
	}
}

// addNormal 登记一笔普通合约（id 序有序）。
func (st *symState) addNormal(c *loan.Contract) {
	st.normal[c.ID] = c
	st.normalQty[c.ID] = c.Qty
	st.lenderNorm[string(c.Lender)] = append(st.lenderNorm[string(c.Lender)], c.ID)
	pos := sort.Search(len(st.normalIDs), func(i int) bool {
		return st.normalIDs[i] >= c.ID
	})
	st.normalIDs = append(st.normalIDs, 0)
	copy(st.normalIDs[pos+1:], st.normalIDs[pos:])
	st.normalIDs[pos] = c.ID
}

func (p *Pool) SetPrice(now int64, sym []byte, price int64) ([]recall.Buy, error) {
	if !validNow(now) || !nonEmpty(sym) || !validQty(price) {
		return nil, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return nil, ErrClock
	}
	buys := p.settle(now) // 入口结算使用旧价
	st := p.sym(string(sym))
	st.price = price
	st.hasPrice = true
	return buys, nil
}

func (p *Pool) Lend(now int64, lender, sym []byte, qty int64) (LendReceipt, error) {
	if !validNow(now) || !nonEmpty(lender) || !nonEmpty(sym) || !validQty(qty) {
		return LendReceipt{}, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return LendReceipt{}, ErrClock
	}
	buys := p.settle(now)
	st := p.sym(string(sym))
	if !st.hasPrice {
		return LendReceipt{Buys: buys}, ErrNotFound
	}
	key := string(lender)
	receipt := LendReceipt{Buys: buys}
	if seq, ok := st.regSeq[key]; ok {
		receipt.RegSeq = seq
	} else {
		st.nextReg++
		st.regSeq[key] = st.nextReg
		receipt.Registered = true
		receipt.RegSeq = st.nextReg
	}
	st.totalLend += qty
	st.totalFree += qty
	st.free[key] += qty
	return receipt, nil
}

// Scanned 返回最近一次成功通过前置校验的 Borrow 实际扫描的存活出借人数。
func (p *Pool) Scanned() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scanned
}

// Price 返回现价与是否已设置。
func (p *Pool) Price(sym []byte) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.syms[string(sym)]
	if st == nil {
		return 0, false
	}
	return st.price, st.hasPrice
}

// Free 返回出借人在该标的的空闲量。
func (p *Pool) Free(lender, sym []byte) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.syms[string(sym)]
	if st == nil {
		return 0
	}
	return st.free[string(lender)]
}

// NormalContracts 返回全部未了结普通合约（合约号升序）。
func (p *Pool) NormalContracts(sym []byte) []loan.Contract {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.syms[string(sym)]
	if st == nil {
		return nil
	}
	out := make([]loan.Contract, 0, len(st.normalIDs))
	for _, id := range st.normalIDs {
		c := st.normal[id]
		out = append(out, loan.Contract{
			ID:       c.ID,
			Lender:   clone(c.Lender),
			Borrower: clone(c.Borrower),
			Sym:      clone(c.Sym),
			Qty:      st.normalQty[id],
		})
	}
	return out
}

// RecallContracts 返回全部未了结召回合约，(dl,id) 升序。
func (p *Pool) RecallContracts() []recall.CContract {
	p.mu.Lock()
	defer p.mu.Unlock()
	all := p.book.All()
	out := make([]recall.CContract, 0, len(all))
	for _, c := range all {
		out = append(out, recall.CContract{
			ID:       c.ID,
			Lender:   clone(c.Lender),
			Borrower: clone(c.Borrower),
			Sym:      clone(c.Sym),
			Qty:      c.Qty,
			DL:       c.DL,
		})
	}
	return out
}

// Debt 返回借入人在某标的的欠券总量。
func (p *Pool) Debt(borrower, sym []byte) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.syms[string(sym)]
	if st == nil {
		return 0
	}
	return st.borrowerDebt[string(borrower)]
}

// Invariant 返回某标的（累计Lend, Σ空闲, Σ未结合约, 累计交还）。
func (p *Pool) Invariant(sym []byte) (totalLend, freeSum, liveSum, returned int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.syms[string(sym)]
	if st == nil {
		return 0, 0, 0, 0
	}
	for _, f := range st.free {
		freeSum += f
	}
	for _, q := range st.normalQty {
		liveSum += q
	}
	for _, rc := range p.book.All() {
		if string(rc.Sym) == string(sym) {
			liveSum += rc.Qty
		}
	}
	return st.totalLend, freeSum, liveSum, st.returned
}

// Buys 返回累计强制买入清单（按发生序）。
func (p *Pool) Buys() []recall.Buy {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recall.Buy, len(p.buyLog))
	copy(out, p.buyLog)
	return out
}

// Now 返回当前时钟。
func (p *Pool) Now() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now
}

func (p *Pool) Return(now int64, borrower, sym []byte, qty int64) (ReturnReceipt, error) {
	if !validNow(now) || !nonEmpty(borrower) || !nonEmpty(sym) || !validQty(qty) {
		return ReturnReceipt{}, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ReturnReceipt{}, ErrClock
	}
	buys := p.settle(now)
	st := p.sym(string(sym))
	bkey := string(borrower)
	if st.borrowerDebt[bkey] <= 0 {
		return ReturnReceipt{Buys: buys}, ErrNotFound
	}
	if qty > st.borrowerDebt[bkey] {
		return ReturnReceipt{Buys: buys}, ErrOverQty
	}

	receipt := ReturnReceipt{Buys: buys}
	need := qty

	// 先还召回：按 (dl, id) 升序，所还直接交还出借人。
	if need > 0 {
		var mine []*recall.CContract
		for _, rc := range p.book.All() {
			if string(rc.Sym) == string(sym) && string(rc.Borrower) == bkey {
				mine = append(mine, rc)
			}
		}
		for _, rc := range mine {
			if need <= 0 {
				break
			}
			take := rc.Qty
			if take > need {
				take = need
			}
			rc.Qty -= take
			st.returned += take
			st.borrowerDebt[bkey] -= take
			receipt.ToRecalled += take
			need -= take
			if rc.Qty <= 0 {
				p.book.Remove(rc.ID)
			}
		}
	}

	// 再还普通：按合约号升序，所还回到出借人空闲。
	if need > 0 {
		var nids []int64
		for _, id := range st.normalIDs {
			c := st.normal[id]
			if c != nil && string(c.Borrower) == bkey {
				nids = append(nids, id)
			}
		}
		for _, id := range nids {
			if need <= 0 {
				break
			}
			c := st.normal[id]
			if c == nil {
				continue
			}
			take := st.normalQty[id]
			if take > need {
				take = need
			}
			lkey := string(c.Lender)
			st.free[lkey] += take
			st.totalFree += take
			st.borrowerDebt[bkey] -= take
			receipt.ToNormal += take
			need -= take
			left := st.normalQty[id] - take
			if left <= 0 {
				st.removeNormal(id)
			} else {
				c.Qty = left
				st.normalQty[id] = left
			}
		}
	}

	return receipt, nil
}

func (p *Pool) Borrow(now int64, borrower, sym []byte, qty int64) (BorrowReceipt, error) {
	if !validNow(now) || !nonEmpty(borrower) || !nonEmpty(sym) || !validQty(qty) {
		return BorrowReceipt{}, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return BorrowReceipt{}, ErrClock
	}
	buys := p.settle(now)
	st := p.sym(string(sym))
	if len(st.regSeq) == 0 {
		return BorrowReceipt{Buys: buys}, ErrNotFound
	}
	if st.totalFree < qty {
		return BorrowReceipt{Buys: buys}, ErrNoSupply
	}

	seqToLender := make(map[int64]string)
	for k, s := range st.regSeq {
		seqToLender[s] = k
	}
	var heap []int64
	for lender, f := range st.free {
		if f > 0 {
			heap = append(heap, st.regSeq[lender])
		}
	}
	sort.Slice(heap, func(i, j int) bool { return heap[i] < heap[j] })

	p.scanned = 0
	need := qty
	var contracts []loan.Contract
	for need > 0 && len(heap) > 0 {
		top := heap[0]
		lender := seqToLender[top]
		avail := st.free[lender]
		if avail <= 0 {
			heap = heap[1:] // 失效堆顶：唯一允许的多碰项
			continue
		}
		p.scanned++
		take := avail
		if take > need {
			take = need
		}
		id := p.newContractID()
		c := loan.Contract{
			ID:       id,
			Lender:   clone([]byte(lender)),
			Borrower: clone(borrower),
			Sym:      clone(sym),
			Qty:      take,
		}
		st.addNormal(&c)
		contracts = append(contracts, c)
		st.free[lender] -= take
		st.totalFree -= take
		st.borrowerDebt[string(borrower)] += take
		need -= take
		if st.free[lender] > 0 {
			down(heap, 0, len(heap)) // 同序号仍在堆中且存活
		} else {
			n := len(heap)
			last := heap[n-1]
			heap = heap[:n-1]
			if n > 1 {
				heap[0] = last
				down(heap, 0, len(heap))
			}
			if len(heap) > 0 {
				down(heap, 0, len(heap))
			}
		}
	}
	return BorrowReceipt{Contracts: contracts, Buys: buys}, nil
}

func (p *Pool) Withdraw(now int64, lender, sym []byte, qty int64) (WithdrawReceipt, error) {
	if !validNow(now) || !nonEmpty(lender) || !nonEmpty(sym) || !validQty(qty) {
		return WithdrawReceipt{}, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return WithdrawReceipt{}, ErrClock
	}
	buys := p.settle(now)
	st := p.sym(string(sym))
	key := string(lender)
	reg, registered := st.regSeq[key]
	_ = reg
	if !registered {
		return WithdrawReceipt{Buys: buys}, ErrNotFound
	}
	var normalHeld int64
	for _, id := range st.lenderNorm[key] {
		normalHeld += st.normalQty[id]
	}
	if qty > st.free[key]+normalHeld {
		return WithdrawReceipt{Buys: buys}, ErrOverQty
	}

	receipt := WithdrawReceipt{Buys: buys}
	need := qty

	// 先取空闲立即交还，离开券池。
	if f := st.free[key]; f > 0 {
		take := f
		if take > need {
			take = need
		}
		st.free[key] -= take
		st.totalFree -= take
		st.returned += take
		need -= take
	}

	// 名下普通合约按合约号降序逐笔：每笔先替换后召回。
	ids := append([]int64(nil), st.lenderNorm[key]...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	removed := map[int64]bool{}

	for _, id := range ids {
		if need <= 0 {
			break
		}
		orig := st.normal[id]
		if orig == nil {
			continue
		}
		remain := st.normalQty[id]

		// 替换：按登记序取其他出借人的空闲，只做这一次。
		type cand struct {
			seq  int64
			name string
		}
		var cands []cand
		for other, f := range st.free {
			if other != key && f > 0 {
				cands = append(cands, cand{st.regSeq[other], other})
			}
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].seq < cands[j].seq })

		for _, cnd := range cands {
			if need <= 0 || remain <= 0 {
				break
			}
			avail := st.free[cnd.name]
			take := avail
			if take > remain {
				take = remain
			}
			if take > need {
				take = need
			}
			st.free[cnd.name] -= take
			st.totalFree -= take
			newID := p.newContractID()
			nc := loan.Contract{
				ID:       newID,
				Lender:   []byte(cnd.name),
				Borrower: clone(orig.Borrower),
				Sym:      clone(sym),
				Qty:      take,
			}
			st.addNormal(&nc)
			receipt.Replacements = append(receipt.Replacements, nc)

			remain -= take
			need -= take
			st.returned += take // 接手券等量立即交还撤回人
		}

		// 仍不足：从原合约拆出召回合约。
		if need > 0 && remain > 0 {
			take := remain
			if take > need {
				take = need
			}
			newID := p.newContractID()
			rc := recall.CContract{
				ID:       newID,
				Lender:   clone(lender),
				Borrower: clone(orig.Borrower),
				Sym:      clone(sym),
				Qty:      take,
				DL:       now + p.n,
			}
			p.book.Add(&rc)
			receipt.Recalls = append(receipt.Recalls, rc)
			remain -= take
			need -= take
		}

		// 原普通合约减量或了结。
		if remain <= 0 {
			delete(st.normal, id)
			delete(st.normalQty, id)
			removed[id] = true
		} else {
			orig.Qty = remain
			st.normalQty[id] = remain
		}
	}
	if len(removed) > 0 {
		kept := st.lenderNorm[key][:0]
		for _, v := range st.lenderNorm[key] {
			if !removed[v] {
				kept = append(kept, v)
			}
		}
		st.lenderNorm[key] = kept
		filtered := st.normalIDs[:0]
		for _, v := range st.normalIDs {
			if !removed[v] {
				filtered = append(filtered, v)
			}
		}
		st.normalIDs = filtered
	}

	return receipt, nil
}
