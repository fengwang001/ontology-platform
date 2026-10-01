package wallet

import (
	"errors"
	"sort"
	"testing"
)

type naiveBatch struct {
	id        int64
	amount    int64
	grantAt   int64
	exp       int64
	sequence  uint64
	remaining int64
	deducted  int64
	refunded  int64
}

type naiveSpend struct {
	lines    []SpendLine
	refunded []int64
}

type naiveWallet struct {
	lastNow int64
	hasNow  bool
	nextSeq uint64
	batches map[int64]*naiveBatch
	spends  map[int64]*naiveSpend
	discard int64
}

type simulatedOperation struct {
	kind   string
	id     int64
	amount int64
	exp    int64
	now    int64
}

func newNaiveWallet() *naiveWallet {
	return &naiveWallet{
		batches: make(map[int64]*naiveBatch),
		spends:  make(map[int64]*naiveSpend),
	}
}

func (n *naiveWallet) sortedBatches() []*naiveBatch {
	result := make([]*naiveBatch, 0, len(n.batches))
	for _, currentBatch := range n.batches {
		result = append(result, currentBatch)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].exp != result[j].exp {
			return result[i].exp < result[j].exp
		}
		if result[i].grantAt != result[j].grantAt {
			return result[i].grantAt < result[j].grantAt
		}
		return result[i].sequence < result[j].sequence
	})
	return result
}

func (n *naiveWallet) grant(id int64, amount int64, exp int64, now int64) error {
	if n.hasNow && now < n.lastNow {
		return ErrNowGoesBack
	}
	if amount <= 0 {
		return ErrNonPositiveAmount
	}
	if _, exists := n.batches[id]; exists {
		return ErrDuplicateBatchID
	}
	if exp <= now {
		return ErrBatchAlreadyExpired
	}

	n.batches[id] = &naiveBatch{
		id:        id,
		amount:    amount,
		grantAt:   now,
		exp:       exp,
		sequence:  n.nextSeq,
		remaining: amount,
	}
	n.nextSeq++
	n.lastNow = now
	n.hasNow = true
	return nil
}

func (n *naiveWallet) spend(sid int64, amount int64, now int64) ([]SpendLine, error) {
	if n.hasNow && now < n.lastNow {
		return nil, ErrNowGoesBack
	}
	if amount <= 0 {
		return nil, ErrNonPositiveAmount
	}
	if _, exists := n.spends[sid]; exists {
		return nil, ErrDuplicateSpendID
	}

	planned := make([]SpendLine, 0)
	left := amount
	for _, currentBatch := range n.sortedBatches() {
		if now >= currentBatch.exp || currentBatch.remaining == 0 {
			continue
		}
		taken := left
		if currentBatch.remaining < taken {
			taken = currentBatch.remaining
		}
		planned = append(planned, SpendLine{BatchID: currentBatch.id, Amount: taken})
		left -= taken
		if left == 0 {
			break
		}
	}
	if left > 0 {
		return nil, ErrInsufficientBalance
	}

	for _, line := range planned {
		currentBatch := n.batches[line.BatchID]
		currentBatch.remaining -= line.Amount
		currentBatch.deducted += line.Amount
	}
	n.spends[sid] = &naiveSpend{
		lines:    planned,
		refunded: make([]int64, len(planned)),
	}
	n.lastNow = now
	n.hasNow = true
	return append([]SpendLine(nil), planned...), nil
}

func (n *naiveWallet) refund(sid int64, amount int64, now int64) ([]RefundLine, error) {
	if n.hasNow && now < n.lastNow {
		return nil, ErrNowGoesBack
	}
	if amount <= 0 {
		return nil, ErrNonPositiveAmount
	}
	record, exists := n.spends[sid]
	if !exists {
		return nil, ErrSpendNotFound
	}

	unrefunded := int64(0)
	for index := range record.lines {
		unrefunded += record.lines[index].Amount - record.refunded[index]
	}
	if amount > unrefunded {
		return nil, ErrRefundTooLarge
	}

	result := make([]RefundLine, 0)
	left := amount
	for index := len(record.lines) - 1; index >= 0 && left > 0; index-- {
		available := record.lines[index].Amount - record.refunded[index]
		returned := left
		if available < returned {
			returned = available
		}
		currentBatch := n.batches[record.lines[index].BatchID]
		voided := now >= currentBatch.exp
		result = append(result, RefundLine{
			BatchID: currentBatch.id,
			Amount:  returned,
			Voided:  voided,
		})
		record.refunded[index] += returned
		currentBatch.refunded += returned
		currentBatch.remaining += returned
		if voided {
			n.discard += returned
		}
		left -= returned
	}

	n.lastNow = now
	n.hasNow = true
	return result, nil
}

func (n *naiveWallet) balance(now int64) (int64, error) {
	if n.hasNow && now < n.lastNow {
		return 0, ErrNowGoesBack
	}
	total := int64(0)
	for _, currentBatch := range n.batches {
		if now < currentBatch.exp {
			total += currentBatch.remaining
		}
	}
	n.lastNow = now
	n.hasNow = true
	return total, nil
}

func TestNaiveSimulationComparison(t *testing.T) {
	actual := New()
	reference := newNaiveWallet()
	operations := []simulatedOperation{
		{kind: "grant", id: 1, amount: 5, exp: 10, now: 0},
		{kind: "grant", id: 2, amount: 7, exp: 10, now: 0},
		{kind: "grant", id: 3, amount: 4, exp: 15, now: 0},
		{kind: "grant", id: 4, amount: 10, exp: 20, now: 0},
		{kind: "spend", id: 1, amount: 10, now: 1},
		{kind: "refund", id: 1, amount: 4, now: 2},
		{kind: "spend", id: 2, amount: 6, now: 3},
		{kind: "refund", id: 1, amount: 6, now: 10},
		{kind: "balance", now: 10},
		{kind: "spend", id: 3, amount: 5, now: 10},
		{kind: "refund", id: 3, amount: 5, now: 15},
		{kind: "balance", now: 15},
		{kind: "spend", id: 9, amount: 11, now: 15},
		{kind: "spend", id: 3, amount: 1, now: 15},
		{kind: "refund", id: 999, amount: 1, now: 15},
		{kind: "refund", id: 2, amount: 7, now: 15},
		{kind: "balance", now: 14},
		{kind: "grant", id: 9, amount: 1, exp: 15, now: 15},
		{kind: "grant", id: 1, amount: 1, exp: 20, now: 15},
		{kind: "balance", now: 15},
	}

	actualDiscard := int64(0)
	for step, operation := range operations {
		switch operation.kind {
		case "grant":
			actualErr := actual.Grant(operation.id, operation.amount, operation.exp, operation.now)
			referenceErr := reference.grant(operation.id, operation.amount, operation.exp, operation.now)
			t.Logf("步骤 %d 输入 Grant(%d,%d,exp=%d,now=%d) => 实际 err=%v；朴素 err=%v；依据：先时间、再数量、批次重复、发放即过期",
				step, operation.id, operation.amount, operation.exp, operation.now, actualErr, referenceErr)
			assertSameError(t, step, actualErr, referenceErr)
		case "spend":
			actualLines, actualErr := actual.Spend(operation.id, operation.amount, operation.now)
			referenceLines, referenceErr := reference.spend(operation.id, operation.amount, operation.now)
			t.Logf("步骤 %d 输入 Spend(sid=%d,amount=%d,now=%d) => 实际 lines=%v err=%v；朴素 lines=%v err=%v；依据：按 exp、grantAt、发放序号逐步扣减",
				step, operation.id, operation.amount, operation.now, actualLines, actualErr, referenceLines, referenceErr)
			assertSameError(t, step, actualErr, referenceErr)
			if !spendLinesEqual(actualLines, referenceLines) {
				t.Fatalf("step %d spend lines actual=%v reference=%v", step, actualLines, referenceLines)
			}
		case "refund":
			actualLines, actualErr := actual.Refund(operation.id, operation.amount, operation.now)
			referenceLines, referenceErr := reference.refund(operation.id, operation.amount, operation.now)
			for _, line := range actualLines {
				if line.Voided {
					actualDiscard += line.Amount
				}
			}
			t.Logf("步骤 %d 输入 Refund(sid=%d,amount=%d,now=%d) => 实际 lines=%v err=%v；朴素 lines=%v err=%v；实际累计过期丢弃=%d，朴素=%d；依据：逆序退回，过期标记作废",
				step, operation.id, operation.amount, operation.now, actualLines, actualErr, referenceLines, referenceErr, actualDiscard, reference.discard)
			assertSameError(t, step, actualErr, referenceErr)
			if !refundLinesEqual(actualLines, referenceLines) {
				t.Fatalf("step %d refund lines actual=%v reference=%v", step, actualLines, referenceLines)
			}
			if actualDiscard != reference.discard {
				t.Fatalf("step %d discarded actual=%d reference=%d", step, actualDiscard, reference.discard)
			}
		case "balance":
			actualBalance, actualErr := actual.Balance(operation.now)
			referenceBalance, referenceErr := reference.balance(operation.now)
			t.Logf("步骤 %d 输入 Balance(now=%d) => 实际 balance=%d err=%v；朴素 balance=%d err=%v；依据：仅累计 now < exp 的剩余量",
				step, operation.now, actualBalance, actualErr, referenceBalance, referenceErr)
			assertSameError(t, step, actualErr, referenceErr)
			if actualBalance != referenceBalance {
				t.Fatalf("step %d balance actual=%d reference=%d", step, actualBalance, referenceBalance)
			}
		}
		assertInvariant(t, actual)
	}
}

func assertSameError(t *testing.T, step int, actual error, reference error) {
	t.Helper()
	if errorName(actual) != errorName(reference) {
		t.Fatalf("step %d error actual=%v reference=%v", step, actual, reference)
	}
}

func errorName(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNowGoesBack):
		return "NowGoesBack"
	case errors.Is(err, ErrNonPositiveAmount):
		return "NonPositiveAmount"
	case errors.Is(err, ErrDuplicateBatchID):
		return "DuplicateBatchID"
	case errors.Is(err, ErrDuplicateSpendID):
		return "DuplicateSpendID"
	case errors.Is(err, ErrSpendNotFound):
		return "SpendNotFound"
	case errors.Is(err, ErrRefundTooLarge):
		return "RefundTooLarge"
	case errors.Is(err, ErrInsufficientBalance):
		return "InsufficientBalance"
	case errors.Is(err, ErrBatchAlreadyExpired):
		return "BatchAlreadyExpired"
	default:
		return err.Error()
	}
}
