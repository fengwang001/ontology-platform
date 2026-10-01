package wallet

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustGrant(t *testing.T, w *Wallet, id int64, amount int64, exp int64, now int64) {
	t.Helper()
	err := w.Grant(id, amount, exp, now)
	t.Logf("输入 Grant(id=%d, amount=%d, exp=%d, now=%d) => 输出 err=%v；判定：exp=%d > now=%d 且 id 未使用", id, amount, exp, now, err, exp, now)
	if err != nil {
		t.Fatalf("Grant(%d): %v", id, err)
	}
	assertInvariant(t, w)
}

func balanceIs(t *testing.T, w *Wallet, now int64, want int64) {
	t.Helper()
	got, err := w.Balance(now)
	t.Logf("输入 Balance(now=%d) => 输出 balance=%d, err=%v；判定：只累计 now < exp 的 remaining", now, got, err)
	if err != nil || got != want {
		t.Fatalf("Balance(%d) = (%d, %v), want %d", now, got, err, want)
	}
	assertInvariant(t, w)
}

func assertInvariant(t *testing.T, w *Wallet) {
	t.Helper()
	for id, b := range w.batches {
		got := b.remaining + b.deducted - b.refunded
		if got != b.amount {
			t.Fatalf("batch %d invariant: remaining=%d + deducted=%d - refunded=%d = %d, want grant amount %d",
				id, b.remaining, b.deducted, b.refunded, got, b.amount)
		}
	}
}

func spendLines(lines []SpendLine) string {
	return fmt.Sprintf("%v", lines)
}

func refundLines(lines []RefundLine) string {
	return fmt.Sprintf("%v", lines)
}

func TestWalletRules(t *testing.T) {
	w := New()

	mustGrant(t, w, 1, 5, 8, 0)
	mustGrant(t, w, 2, 7, 8, 1)
	mustGrant(t, w, 3, 10, 10, 2)
	mustGrant(t, w, 4, 8, 20, 3)

	balanceIs(t, w, 5, 30)

	lines, err := w.Spend(100, 10, 5)
	t.Logf("输入 Spend(sid=100, amount=10, now=5) => 输出 lines=%s, err=%v；判定：批次 1、2 同为 exp=8，按发放先后，跨批扣 5+5", spendLines(lines), err)
	if err != nil {
		t.Fatalf("Spend(100): %v", err)
	}
	wantSpend := []SpendLine{{BatchID: 1, Amount: 5}, {BatchID: 2, Amount: 5}}
	if !spendLinesEqual(lines, wantSpend) {
		t.Fatalf("Spend(100) = %v, want %v", lines, wantSpend)
	}
	assertInvariant(t, w)

	refunded, err := w.Refund(100, 3, 5)
	t.Logf("输入 Refund(sid=100, amount=3, now=5) => 输出 lines=%s, err=%v；判定：逆序先退消费明细最后一批 2", refundLines(refunded), err)
	if err != nil {
		t.Fatalf("Refund(100, 3): %v", err)
	}
	wantRefund := []RefundLine{{BatchID: 2, Amount: 3, Voided: false}}
	if !refundLinesEqual(refunded, wantRefund) {
		t.Fatalf("Refund(100, 3) = %v, want %v", refunded, wantRefund)
	}

	refunded, err = w.Refund(100, 7, 5)
	t.Logf("输入 Refund(sid=100, amount=7, now=5) => 输出 lines=%s, err=%v；判定：继续逆序，先退批次 2 剩余可退 2，再退批次 1 的 5", refundLines(refunded), err)
	if err != nil {
		t.Fatalf("Refund(100, 7): %v", err)
	}
	wantRefund = []RefundLine{
		{BatchID: 2, Amount: 2, Voided: false},
		{BatchID: 1, Amount: 5, Voided: false},
	}
	if !refundLinesEqual(refunded, wantRefund) {
		t.Fatalf("Refund(100, 7) = %v, want %v", refunded, wantRefund)
	}

	lines, err = w.Spend(101, 8, 9)
	t.Logf("输入 Spend(sid=101, amount=8, now=9) => 输出 lines=%s, err=%v；判定：批次 1、2 在 exp=8 后已不可消费，批次 3 最早到期，扣批次 3", spendLines(lines), err)
	if err != nil {
		t.Fatalf("Spend(101): %v", err)
	}
	wantSpend = []SpendLine{{BatchID: 3, Amount: 8}}
	if !spendLinesEqual(lines, wantSpend) {
		t.Fatalf("Spend(101) = %v, want %v", lines, wantSpend)
	}

	balanceIs(t, w, 9, 10)
	balanceIs(t, w, 10, 8)

	refunded, err = w.Refund(101, 8, 10)
	discarded := int64(0)
	for _, line := range refunded {
		if line.Voided {
			discarded += line.Amount
		}
	}
	t.Logf("输入 Refund(sid=101, amount=8, now=10) => 输出 lines=%s, err=%v；判定：now 恰等于 exp=10，批次 3 已过期，退回 8 全部作废，过期丢弃=%d", refundLines(refunded), err, discarded)
	if err != nil {
		t.Fatalf("Refund(101): %v", err)
	}
	wantRefund = []RefundLine{{BatchID: 3, Amount: 8, Voided: true}}
	if !refundLinesEqual(refunded, wantRefund) || discarded != 8 {
		t.Fatalf("void refund = %v, discarded=%d, want %v and 8", refunded, discarded, wantRefund)
	}
	balanceIs(t, w, 10, 8)

	_, err = w.Spend(102, 9, 10)
	t.Logf("输入 Spend(sid=102, amount=9, now=10) => 输出 err=%v；判定：有效余额 8 不足，提交前拒绝，批次和消费单零改动", err)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("Spend(102) err = %v, want ErrInsufficientBalance", err)
	}
	balanceIs(t, w, 10, 8)
	if _, missingErr := w.Refund(102, 1, 10); !errors.Is(missingErr, ErrSpendNotFound) {
		t.Fatalf("rejected Spend left spend record; Refund(102) err = %v, want ErrSpendNotFound", missingErr)
	}

	_, err = w.Balance(9)
	t.Logf("输入 Balance(now=9) => 输出 err=%v；判定：9 小于此前操作 now=10，拒绝且不推进时间线", err)
	if !errors.Is(err, ErrNowGoesBack) {
		t.Fatalf("Balance(9) err = %v, want ErrNowGoesBack", err)
	}
	if balance, err := w.Balance(10); err != nil || balance != 8 {
		t.Fatalf("Balance after rejected rollback = (%d, %v), want 8", balance, err)
	}
	assertInvariant(t, w)
}

func TestRejectionOrderAndImmediateExpiry(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 10, 10, 5)

	err := w.Grant(2, 0, 10, 4)
	t.Logf("输入 Grant(id=2, amount=0, exp=10, now=4) => 输出 err=%v；判定：拒绝顺序中 now 回退先于数量非正", err)
	if !errors.Is(err, ErrNowGoesBack) {
		t.Fatalf("got %v, want ErrNowGoesBack", err)
	}

	err = w.Grant(2, 0, 10, 5)
	t.Logf("输入 Grant(id=2, amount=0, exp=10, now=5) => 输出 err=%v；判定：时间合法后先报数量非正", err)
	if !errors.Is(err, ErrNonPositiveAmount) {
		t.Fatalf("got %v, want ErrNonPositiveAmount", err)
	}

	err = w.Grant(1, 10, 10, 5)
	t.Logf("输入 Grant(id=1, amount=10, exp=10, now=5) => 输出 err=%v；判定：id=1 已使用", err)
	if !errors.Is(err, ErrDuplicateBatchID) {
		t.Fatalf("got %v, want ErrDuplicateBatchID", err)
	}

	err = w.Grant(2, 10, 5, 5)
	t.Logf("输入 Grant(id=2, amount=10, exp=5, now=5) => 输出 err=%v；判定：左闭右开，exp <= now 为发放即过期", err)
	if !errors.Is(err, ErrBatchAlreadyExpired) {
		t.Fatalf("got %v, want ErrBatchAlreadyExpired", err)
	}

	if _, err := w.Spend(100, 10, 5); err != nil {
		t.Fatalf("setup Spend(100): %v", err)
	}
	_, err = w.Spend(100, 1, 5)
	t.Logf("输入 Spend(sid=100, amount=1, now=5) => 输出 err=%v；判定：消费单 id 重复", err)
	if !errors.Is(err, ErrDuplicateSpendID) {
		t.Fatalf("got %v, want ErrDuplicateSpendID", err)
	}

	_, err = w.Refund(999, 1, 5)
	t.Logf("输入 Refund(sid=999, amount=1, now=5) => 输出 err=%v；判定：消费单不存在", err)
	if !errors.Is(err, ErrSpendNotFound) {
		t.Fatalf("got %v, want ErrSpendNotFound", err)
	}

	_, err = w.Refund(100, 11, 5)
	t.Logf("输入 Refund(sid=100, amount=11, now=5) => 输出 err=%v；判定：超过尚未退回数量 10", err)
	if !errors.Is(err, ErrRefundTooLarge) {
		t.Fatalf("got %v, want ErrRefundTooLarge", err)
	}

	_, err = w.Spend(101, 11, 5)
	t.Logf("输入 Spend(sid=101, amount=11, now=5) => 输出 err=%v；判定：批次 1 有效余额为 0，余额不足", err)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("got %v, want ErrInsufficientBalance", err)
	}
	assertInvariant(t, w)
}

func TestConcurrentOperations(t *testing.T) {
	w := New()
	const goroutines = 32
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		index := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := int64(index + 1)
			if err := w.Grant(id, 10, 100, 10); err != nil {
				t.Errorf("Grant(%d): %v", id, err)
			}
		}()
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		index := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sid := int64(index + 1000)
			lines, err := w.Spend(sid, 10, 20)
			if err != nil {
				t.Errorf("Spend(%d): %v", index+1000, err)
				return
			}
			if got := sumSpend(lines); got != 10 {
				t.Errorf("Spend(%d) total = %d, want 10", sid, got)
			}
		}()
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		sid := int64(i + 1000)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := w.Refund(sid, 10, 30); err != nil {
				t.Errorf("Refund(%d): %v", sid, err)
			}
		}()
	}
	wg.Wait()

	balance, err := w.Balance(40)
	t.Logf("并发发放/消费/全额退回后 Balance(now=40)=%d, err=%v；判定：所有操作互斥串行，退回恢复全部 320", balance, err)
	if err != nil || balance != 320 {
		t.Fatalf("concurrent balance = (%d, %v), want 320", balance, err)
	}
	assertInvariant(t, w)
}

func sumSpend(lines []SpendLine) int64 {
	total := int64(0)
	for _, line := range lines {
		total += line.Amount
	}
	return total
}

func spendLinesEqual(a []SpendLine, b []SpendLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func refundLinesEqual(a []RefundLine, b []RefundLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
