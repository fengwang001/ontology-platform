package sms

import (
	"sync"
	"testing"
)

func TestConcurrentDepositsAndSends(t *testing.T) {
	meter := newTestMeter(t, 100, 1, 10, 6, 100)
	const goroutines = 100
	const depositAmount = 100

	var group sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if err := meter.Deposit("u"+itoa(index), depositAmount); err != nil {
				t.Error(err)
			}
		}(index)
	}
	group.Wait()

	costs := make([]int64, goroutines)
	for index := 0; index < goroutines; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			result, err := meter.Send("u"+itoa(index), repeatRune('a', 161), false, 0)
			if err != nil {
				t.Error(err)
				return
			}
			costs[index] = result.Cost
		}(index)
	}
	group.Wait()

	var totalBalance int64
	var totalCost int64
	for index := 0; index < goroutines; index++ {
		record := meter.accounts["u"+itoa(index)]
		if record == nil || record.balance < 0 {
			t.Fatalf("account %d has invalid state %+v", index, record)
		}
		totalBalance += record.balance
		totalCost += costs[index]
		if record.balance+costs[index] != depositAmount {
			t.Fatalf("account %d balance %d + cost %d != deposit %d", index, record.balance, costs[index], depositAmount)
		}
	}
	if totalBalance+totalCost != goroutines*depositAmount {
		t.Fatalf("invariant = %d + %d, want %d", totalBalance, totalCost, goroutines*depositAmount)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 12)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
