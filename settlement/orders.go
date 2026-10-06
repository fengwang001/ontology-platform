package settlement

import (
	"container/heap"
	"math/big"
)

func (s *System) RegisterOrder(input OrderInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Printf("register order input: %+v", input)
	if !validOrderInput(input) {
		err := ErrInvalidParameter
		s.log.Printf("register order rejected: %v", err)
		return err
	}
	if _, isBusinessDay := s.businessSet[input.SettlementDay]; !isBusinessDay {
		err := ErrInvalidParameter
		s.log.Printf("register order rejected: %v settlement day=%d", err, input.SettlementDay)
		return err
	}
	if _, exists := s.orders[input.ID]; exists {
		err := ErrDuplicateOrder
		s.log.Printf("register order rejected: %v", err)
		return err
	}
	if _, buyerExists := s.accounts[input.Buyer]; !buyerExists {
		err := ErrAccountNotFound
		s.log.Printf("register order rejected: %v buyer=%s", err, input.Buyer)
		return err
	}
	if _, sellerExists := s.accounts[input.Seller]; !sellerExists {
		err := ErrAccountNotFound
		s.log.Printf("register order rejected: %v seller=%s", err, input.Seller)
		return err
	}
	if s.lastIndex >= 0 && input.SettlementDay < s.businessDays[s.lastIndex] {
		err := ErrDatePassed
		s.log.Printf("register order rejected: %v day=%d last=%d", err, input.SettlementDay, s.businessDays[s.lastIndex])
		return err
	}

	order := &Order{
		Input:     input,
		Remaining: input.Quantity,
		Status:    StatusPending,
	}
	s.orders[input.ID] = order
	if s.byDue[input.SettlementDay] == nil {
		s.byDue[input.SettlementDay] = make(map[uint64]*Order)
		heap.Push(&s.dueHeap, input.SettlementDay)
	}
	s.byDue[input.SettlementDay][input.ID] = order

	s.log.Printf("register order accepted id=%d due=%d status=%s", order.Input.ID, order.Input.SettlementDay, order.Status)
	return nil
}

func validOrderInput(input OrderInput) bool {
	if input.Security == "" || input.Buyer == "" || input.Seller == "" || input.Buyer == input.Seller {
		return false
	}
	if input.Quantity <= 0 || input.Price <= 0 {
		return false
	}
	if !fitsProduct(input.Quantity, input.Price) {
		return false
	}
	return true
}

func fitsProduct(a, b int64) bool {
	return new(big.Int).Mul(big.NewInt(a), big.NewInt(b)).IsInt64()
}
