package wallet

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrNowGoesBack         = errors.New("wallet: now is earlier than the previous operation")
	ErrNonPositiveAmount   = errors.New("wallet: amount must be positive")
	ErrDuplicateBatchID    = errors.New("wallet: batch id already exists")
	ErrDuplicateSpendID    = errors.New("wallet: spend id already exists")
	ErrSpendNotFound       = errors.New("wallet: spend id does not exist")
	ErrRefundTooLarge      = errors.New("wallet: refund amount exceeds unrefunded spend amount")
	ErrInsufficientBalance = errors.New("wallet: available balance is insufficient")
	ErrBatchAlreadyExpired = errors.New("wallet: expiration time must be later than grant time")
)

type SpendLine struct {
	BatchID int64
	Amount  int64
}

type RefundLine struct {
	BatchID int64
	Amount  int64
	Voided  bool
}

type batch struct {
	id      int64
	amount  int64
	grantAt int64
	exp     int64
	seq     uint64

	remaining int64
	deducted  int64
	refunded  int64
}

type spendRecord struct {
	lines    []SpendLine
	refunded []int64
}

type Wallet struct {
	mu      sync.Mutex
	lastNow int64
	hasNow  bool
	nextSeq uint64
	batches map[int64]*batch
	spends  map[int64]*spendRecord
}

func New() *Wallet {
	return &Wallet{
		batches: make(map[int64]*batch),
		spends:  make(map[int64]*spendRecord),
	}
}

func (w *Wallet) Grant(id int64, amount int64, exp int64, now int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.hasNow && now < w.lastNow {
		return ErrNowGoesBack
	}
	if amount <= 0 {
		return ErrNonPositiveAmount
	}
	if _, exists := w.batches[id]; exists {
		return ErrDuplicateBatchID
	}
	if exp <= now {
		return ErrBatchAlreadyExpired
	}

	w.batches[id] = &batch{
		id:        id,
		amount:    amount,
		grantAt:   now,
		exp:       exp,
		seq:       w.nextSeq,
		remaining: amount,
	}
	w.nextSeq++
	w.lastNow = now
	w.hasNow = true
	return nil
}

func (w *Wallet) Spend(sid int64, amount int64, now int64) ([]SpendLine, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.hasNow && now < w.lastNow {
		return nil, ErrNowGoesBack
	}
	if amount <= 0 {
		return nil, ErrNonPositiveAmount
	}
	if _, exists := w.spends[sid]; exists {
		return nil, ErrDuplicateSpendID
	}

	lines := make([]SpendLine, 0)
	remainingToSpend := amount
	for _, currentBatch := range sortedBatches(w.batches) {
		if now >= currentBatch.exp || currentBatch.remaining == 0 {
			continue
		}
		taken := minInt64(remainingToSpend, currentBatch.remaining)
		if taken > 0 {
			lines = append(lines, SpendLine{
				BatchID: currentBatch.id,
				Amount:  taken,
			})
			remainingToSpend -= taken
		}
		if remainingToSpend == 0 {
			break
		}
	}

	if remainingToSpend > 0 {
		return nil, ErrInsufficientBalance
	}

	for index := range lines {
		currentBatch := w.batches[lines[index].BatchID]
		currentBatch.remaining -= lines[index].Amount
		currentBatch.deducted += lines[index].Amount
	}
	w.spends[sid] = &spendRecord{
		lines:    lines,
		refunded: make([]int64, len(lines)),
	}
	w.lastNow = now
	w.hasNow = true
	return append([]SpendLine(nil), lines...), nil
}

func (w *Wallet) Refund(sid int64, amount int64, now int64) ([]RefundLine, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.hasNow && now < w.lastNow {
		return nil, ErrNowGoesBack
	}
	if amount <= 0 {
		return nil, ErrNonPositiveAmount
	}
	record, exists := w.spends[sid]
	if !exists {
		return nil, ErrSpendNotFound
	}

	unrefundedTotal := int64(0)
	for index := range record.lines {
		unrefundedTotal += record.lines[index].Amount - record.refunded[index]
	}
	if amount > unrefundedTotal {
		return nil, ErrRefundTooLarge
	}

	lines := make([]RefundLine, 0)
	remainingToRefund := amount
	for index := len(record.lines) - 1; index >= 0 && remainingToRefund > 0; index-- {
		available := record.lines[index].Amount - record.refunded[index]
		returned := minInt64(remainingToRefund, available)
		if returned == 0 {
			continue
		}

		currentBatch := w.batches[record.lines[index].BatchID]
		voided := now >= currentBatch.exp
		lines = append(lines, RefundLine{
			BatchID: currentBatch.id,
			Amount:  returned,
			Voided:  voided,
		})

		record.refunded[index] += returned
		currentBatch.refunded += returned
		currentBatch.remaining += returned
		remainingToRefund -= returned
	}

	w.lastNow = now
	w.hasNow = true
	return lines, nil
}

func (w *Wallet) Balance(now int64) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.hasNow && now < w.lastNow {
		return 0, ErrNowGoesBack
	}

	var balance int64
	for _, currentBatch := range w.batches {
		if now < currentBatch.exp {
			balance += currentBatch.remaining
		}
	}
	w.lastNow = now
	w.hasNow = true
	return balance, nil
}

func sortedBatches(batches map[int64]*batch) []*batch {
	result := make([]*batch, 0, len(batches))
	for _, currentBatch := range batches {
		result = append(result, currentBatch)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].exp != result[j].exp {
			return result[i].exp < result[j].exp
		}
		if result[i].grantAt != result[j].grantAt {
			return result[i].grantAt < result[j].grantAt
		}
		return result[i].seq < result[j].seq
	})
	return result
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
