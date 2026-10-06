package deposit

import (
	"math/big"
	"sync"
)

// NewService constructs a concurrency-safe service.  A, B, C must be
// non-negative; the penalty rate must be non-negative with a positive
// denominator.
func NewService(cfg Config) *Service {
	if cfg.A < 0 || cfg.B < 0 || cfg.C < 0 || cfg.RateNum < 0 || cfg.RateDen <= 0 {
		panic("deposit: invalid config")
	}
	return &Service{
		mu:     &sync.Mutex{},
		cfg:    cfg,
		leases: make(map[string]*lease),
	}
}

func (s *Service) lock() *sync.Mutex { return s.mu.(*sync.Mutex) }

func validID(id string) bool   { return id != "" }
func validCat(c Category) bool { return c >= Rent && c <= Other }

// CreateLease registers a lease with a positive integer deposit.
func (s *Service) CreateLease(id string, deposit int64, now int) error {
	if !validID(id) {
		return reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	if _, exists := s.leases[id]; exists {
		return reject(ErrInvalidParameter)
	}
	if deposit <= 0 {
		return reject(ErrAmountOutOfRange)
	}
	s.leases[id] = newLease(id, deposit, now)
	return nil
}

// Checkout records the checkout day.  The lease starts unchecked; checkout is
// idempotent only if the day is unchanged.
func (s *Service) Checkout(id string, now int) error {
	if !validID(id) {
		return reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	l, ok := s.leases[id]
	if !ok {
		return reject(ErrLeaseNotFound)
	}
	if now < l.lastNow {
		return reject(ErrClockBackward)
	}
	if l.checked {
		return reject(ErrIllegalState)
	}
	l.checked = true
	l.checkout = now
	l.lastNow = now
	return nil
}

// getLeaseForOp performs the shared first-error lookup chain:
// parameter -> clock -> existence -> checkout.
func (s *Service) getLeaseForOp(id string, now int) (*lease, error) {
	l, ok := s.leases[id]
	if !ok {
		return nil, reject(ErrLeaseNotFound)
	}
	if now < l.lastNow {
		return nil, reject(ErrClockBackward)
	}
	if !l.checked {
		return nil, reject(ErrNotCheckedOut)
	}
	return l, nil
}

// Declare registers one deduction on days [checkout, checkout+A].
func (s *Service) Declare(id string, cat Category, amount int64, now int) (int, error) {
	if !validID(id) || !validCat(cat) {
		return 0, reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	l, err := s.getLeaseForOp(id, now)
	if err != nil {
		return 0, err
	}
	if amount <= 0 {
		return 0, reject(ErrAmountOutOfRange)
	}
	if now > l.declClose(s.cfg) {
		return 0, reject(ErrDeclarationLate)
	}

	it := &deduction{
		id:       l.nextID,
		cat:      cat,
		amount:   amount,
		declared: now,
		awarded:  -1,
	}
	l.nextID++
	it.order = len(l.seq)
	l.items[it.id] = it
	l.seq = append(l.seq, it)
	l.lastNow = now
	return it.id, nil
}

// Revoke withdraws a deduction while the declaration window is still open.
// Revocation only succeeds before close; after close the allocation is fixed.
func (s *Service) Revoke(id string, dedID int, now int) error {
	if !validID(id) || dedID <= 0 {
		return reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	l, err := s.getLeaseForOp(id, now)
	if err != nil {
		return err
	}
	it, ok := l.items[dedID]
	if !ok {
		return reject(ErrIllegalState)
	}
	if now > l.declClose(s.cfg) {
		return reject(ErrDeclarationLate)
	}
	if it.revoked {
		return reject(ErrIllegalState)
	}
	it.revoked = true
	l.lastNow = now
	return nil
}

// Dispute freezes the satisfied amount of one deduction.  Disputes are
// accepted on days [declClose+1, declClose+B], at most once per item.
func (s *Service) Dispute(id string, dedID int, now int) error {
	if !validID(id) || dedID <= 0 {
		return reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	l, err := s.getLeaseForOp(id, now)
	if err != nil {
		return err
	}
	it, ok := l.items[dedID]
	if !ok {
		return reject(ErrIllegalState)
	}
	if now <= l.declClose(s.cfg) {
		return reject(ErrDisputeLate)
	}
	if now > l.disputeClose(s.cfg) {
		return reject(ErrDisputeLate)
	}
	if it.revoked {
		return reject(ErrIllegalState)
	}
	if it.disputed {
		return reject(ErrIllegalState)
	}

	// First post-close action pays the one-time O(n) finalization cost.
	l.finalize(s.cfg)

	// O(1) per item: move the frozen allocation out of the landlord bucket.
	l.landlord -= it.satisfied
	l.frozen += it.satisfied
	it.disputed = true
	l.lastNow = now
	return nil
}

// Adjudicate settles a dispute.  award is clamped by validation to
// [0, original amount].  Only this item's disposition changes; the release
// becomes a new C-day refund tranche and any award above the frozen amount
// stays a receivable and never touches the deposit again.
func (s *Service) Adjudicate(id string, dedID int, award int64, now int) error {
	if !validID(id) || dedID <= 0 {
		return reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	l, err := s.getLeaseForOp(id, now)
	if err != nil {
		return err
	}
	it, ok := l.items[dedID]
	if !ok {
		return reject(ErrIllegalState)
	}
	if !it.disputed {
		return reject(ErrIllegalState)
	}
	if it.adjudged {
		return reject(ErrIllegalState)
	}
	if award < 0 || award > it.amount {
		return reject(ErrAmountOutOfRange)
	}
	l.finalize(s.cfg)

	frozen := it.satisfied
	upheld := award
	if upheld > frozen {
		upheld = frozen
	}
	released := frozen - upheld

	// O(1) per item: split the frozen bucket, no other item is touched.
	l.frozen -= frozen
	l.landlord += upheld
	it.adjudged = true
	it.awarded = award
	l.addBatch(released, now+s.cfg.C)
	l.lastNow = now
	return nil
}

// Refund pays out every currently available tranche in one atomic operation.
// Partial refunds and zero-amount refunds are rejected.
func (s *Service) Refund(id string, now int) (*RefundRecord, error) {
	if !validID(id) {
		return nil, reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	l, err := s.getLeaseForOp(id, now)
	if err != nil {
		return nil, err
	}
	if now <= l.declClose(s.cfg) {
		return nil, reject(ErrIllegalState)
	}
	l.finalize(s.cfg)

	var total int64
	pen := new(big.Rat)
	var paying []*batch
	for _, b := range l.batches {
		if b.paid {
			continue
		}
		paying = append(paying, b)
		total += b.amount
		pen.Add(pen, penalty(b.amount, b.deadline, now, s.cfg))
	}
	if len(paying) == 0 || total == 0 {
		return nil, reject(ErrIllegalState)
	}

	for _, b := range paying {
		b.paid = true
		b.paidDay = now
		b.penalty = penalty(b.amount, b.deadline, now, s.cfg)
	}
	l.refunded += total
	l.awaiting -= total
	rec := &RefundRecord{Day: now, Amount: total, Penalty: pen}
	l.lastNow = now
	// Keep an audit trail.
	l.refunds = append(l.refunds, *rec)
	return rec, nil
}

// Snapshot reports the full state at day now without accepting an operation,
// so it never advances lastNow; a backwards query is still rejected.
func (s *Service) Snapshot(id string, now int) (*Snapshot, error) {
	if !validID(id) {
		return nil, reject(ErrInvalidParameter)
	}
	s.lock().Lock()
	defer s.lock().Unlock()

	l, ok := s.leases[id]
	if !ok {
		return nil, reject(ErrLeaseNotFound)
	}
	if now < l.lastNow {
		return nil, reject(ErrClockBackward)
	}
	if !l.checked {
		return nil, reject(ErrNotCheckedOut)
	}

	var refundable int64
	pen := new(big.Rat)
	for _, b := range l.batches {
		if b.paid {
			continue
		}
		refundable += b.amount
		pen.Add(pen, penalty(b.amount, b.deadline, now, s.cfg))
	}

	items := make([]Deduction, 0, len(l.seq))
	for _, it := range l.seq {
		items = append(items, Deduction{
			ID: it.id, Category: it.cat, Amount: it.amount, Declared: it.declared,
			Revoked: it.revoked, Disputed: it.disputed, Adjudicated: it.adjudged,
			Awarded: it.awarded, Satisfied: it.satisfied,
		})
	}
	refunds := append([]RefundRecord(nil), l.refunds...)

	return &Snapshot{
		Now: now, Deposit: l.deposit, CheckoutDay: l.checkout,
		Refunded: l.refunded, Landlord: l.landlord, Frozen: l.frozen, Awaiting: l.awaiting,
		Pending: l.pending(), Receivable: l.receivable(),
		Refundable: refundable, PenaltyDue: pen,
		Deductions: items, Refunds: refunds,
	}, nil
}
