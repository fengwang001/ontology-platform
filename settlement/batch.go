package settlement

import (
	"container/heap"
	"math/big"
	"sort"
)

type plannedFill struct {
	order            *Order
	delivered        int64
	remaining        int64
	sellerAvailable  int64
	buyerAffordable  int64
	settlementAmount int64
	responsible      ResponsibleParty
	penalty          int64
	force            bool
	compensation     int64
	referencePrice   int64
}

func (s *System) ProcessBusinessDay(day int, referencePrices map[SecurityID]int64) (ProcessResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Printf("process day input day=%d reference_prices=%v", day, referencePrices)
	if referencePrices == nil {
		return ProcessResult{}, ErrInvalidParameter
	}

	orders, poppedDueDays := s.ordersForDay(day)
	for _, order := range orders {
		price, ok := referencePrices[order.Input.Security]
		if !ok || price <= 0 {
			s.restoreDueDays(poppedDueDays)
			return ProcessResult{}, ErrInvalidParameter
		}
		if !fitsProduct(price, order.Remaining) {
			s.restoreDueDays(poppedDueDays)
			return ProcessResult{}, ErrInvalidParameter
		}
	}

	dayIndex, isBusinessDay := s.businessIndex(day)
	if !isBusinessDay {
		s.restoreDueDays(poppedDueDays)
		return ProcessResult{}, ErrNonBusinessDay
	}
	if dayIndex != s.lastIndex+1 {
		s.restoreDueDays(poppedDueDays)
		return ProcessResult{}, ErrOutOfOrder
	}

	plans := s.planFills(orders)
	if len(plans) != len(orders) {
		s.restoreDueDays(poppedDueDays)
		return ProcessResult{}, ErrInvalidParameter
	}
	if err := s.prepareCompensations(plans, referencePrices); err != nil {
		s.restoreDueDays(poppedDueDays)
		return ProcessResult{}, err
	}
	if err := s.validateLedgerPostings(plans); err != nil {
		s.restoreDueDays(poppedDueDays)
		return ProcessResult{}, err
	}
	result := s.applyPlans(day, plans, referencePrices)
	s.lastIndex = dayIndex
	s.cleanupDay(day, poppedDueDays)

	s.log.Printf("process day output day=%d fills=%d penalties=%d force_closures=%d", day, len(result.Processed), len(result.Penalties), len(result.ForceClosures))
	return result, nil
}

func (s *System) businessIndex(day int) (int, bool) {
	index := sort.SearchInts(s.businessDays, day)
	if index == len(s.businessDays) || s.businessDays[index] != day {
		return 0, false
	}
	return index, true
}

func (s *System) ordersForDay(day int) ([]*Order, []int) {
	orders := append([]*Order(nil), s.active...)
	poppedDueDays := make([]int, 0)
	for s.dueHeap.Len() > 0 && s.dueHeap[0] <= day {
		dueDay := heap.Pop(&s.dueHeap).(int)
		bucket := s.byDue[dueDay]
		for _, order := range bucket {
			if order.Remaining <= 0 {
				continue
			}
			orders = append(orders, order)
		}
		poppedDueDays = append(poppedDueDays, dueDay)
	}

	sort.Slice(orders, func(i, j int) bool {
		return orders[i].Input.ID < orders[j].Input.ID
	})
	return orders, poppedDueDays
}

func (s *System) planFills(orders []*Order) []plannedFill {
	securityReserved := make(map[AccountID]map[SecurityID]int64)
	cashReserved := make(map[AccountID]int64)
	plans := make([]plannedFill, 0, len(orders))

	for _, order := range orders {
		seller := s.accounts[order.Input.Seller]
		buyer := s.accounts[order.Input.Buyer]
		requested := order.Remaining

		sellerStart := seller.holdings[order.Input.Security]
		sellerReserved := int64(0)
		if reserved := securityReserved[order.Input.Seller][order.Input.Security]; reserved > 0 {
			sellerReserved = reserved
		}
		sellerAvailable := sellerStart - sellerReserved
		buyerAvailableCash := buyer.cash - cashReserved[order.Input.Buyer]
		buyerAffordable := buyerAvailableCash / order.Input.Price

		delivered := min64(requested, sellerAvailable, buyerAffordable)
		if !order.Input.AllowPartial && delivered < order.Remaining {
			delivered = 0
		}
		settlementAmount := delivered * order.Input.Price
		remaining := order.Remaining - delivered

		responsible := ResponsibleBuyer
		if sellerAvailable < requested {
			responsible = ResponsibleSeller
		}

		if delivered > 0 {
			if securityReserved[order.Input.Seller] == nil {
				securityReserved[order.Input.Seller] = make(map[SecurityID]int64)
			}
			securityReserved[order.Input.Seller][order.Input.Security] += delivered
			cashReserved[order.Input.Buyer] += settlementAmount
		}

		plan := plannedFill{
			order:            order,
			delivered:        delivered,
			sellerAvailable:  sellerAvailable,
			buyerAffordable:  buyerAffordable,
			settlementAmount: settlementAmount,
			responsible:      responsible,
		}
		plan.remaining = remaining

		if remaining > 0 {
			cashAmount := order.Input.Price * remaining
			penaltyNumerator, overflow := multiply(cashAmount, s.penaltyBPS)
			if overflow {
				plan.penalty = -1
			} else {
				plan.penalty = ceilDiv(penaltyNumerator, 10000)
			}
			plan.force = order.FailedDays+1 >= s.maxFailDays
		}

		plans = append(plans, plan)
	}

	return plans
}

func (s *System) applyPlans(day int, plans []plannedFill, referencePrices map[SecurityID]int64) ProcessResult {
	result := ProcessResult{
		Day:           day,
		Processed:     make([]FillResult, 0, len(plans)),
		Penalties:     nil,
		ForceClosures: nil,
	}
	activeAfter := make([]*Order, 0, len(plans))
	securityDelta := make(map[AccountID]map[SecurityID]int64)
	cashDelta := make(map[AccountID]int64)

	for _, plan := range plans {
		order := plan.order
		remainingBefore := order.Remaining

		if plan.delivered > 0 {
			if securityDelta[order.Input.Seller] == nil {
				securityDelta[order.Input.Seller] = make(map[SecurityID]int64)
			}
			if securityDelta[order.Input.Buyer] == nil {
				securityDelta[order.Input.Buyer] = make(map[SecurityID]int64)
			}
			securityDelta[order.Input.Seller][order.Input.Security] -= plan.delivered
			securityDelta[order.Input.Buyer][order.Input.Security] += plan.delivered
			cashDelta[order.Input.Seller] += plan.settlementAmount
			cashDelta[order.Input.Buyer] -= plan.settlementAmount

			order.Delivered += plan.delivered
			order.Remaining -= plan.delivered
		}

		fill := FillResult{
			OrderID:             order.Input.ID,
			Security:            order.Input.Security,
			Buyer:               order.Input.Buyer,
			Seller:              order.Input.Seller,
			Requested:           remainingBefore,
			Delivered:           plan.delivered,
			Remaining:           order.Remaining,
			SellerAvailable:     plan.sellerAvailable,
			BuyerAffordable:     plan.buyerAffordable,
			SettlementAmount:    plan.settlementAmount,
			ResponsibleIfFailed: plan.responsible,
		}
		result.Processed = append(result.Processed, fill)
		s.log.Printf("decision day=%d order=%d requested=%d seller_available=%d buyer_affordable=%d delivered=%d remaining=%d responsible_if_failed=%s", day, order.Input.ID, remainingBefore, plan.sellerAvailable, plan.buyerAffordable, plan.delivered, order.Remaining, plan.responsible)

		if order.Remaining == 0 {
			order.Status = StatusCompleted
			continue
		}

		order.FailedDays++
		if order.Delivered > 0 {
			order.Status = StatusPartial
		} else {
			order.Status = StatusPending
		}

		responsibleAccount := order.Input.Buyer
		counterparty := order.Input.Seller
		if plan.responsible == ResponsibleSeller {
			responsibleAccount = order.Input.Seller
			counterparty = order.Input.Buyer
		}
		cashAmount := order.Input.Price * order.Remaining
		s.accounts[responsibleAccount].penaltyPayable += plan.penalty
		s.accounts[counterparty].penaltyReceivable += plan.penalty
		result.Penalties = append(result.Penalties, PenaltyResult{
			OrderID:      order.Input.ID,
			Responsible:  responsibleAccount,
			Counterparty: counterparty,
			Remaining:    order.Remaining,
			CashAmount:   cashAmount,
			PenaltyBPS:   s.penaltyBPS,
			Amount:       plan.penalty,
		})
		s.log.Printf("penalty day=%d order=%d responsible=%s remaining=%d cash_amount=%d bps=%d amount=%d", day, order.Input.ID, responsibleAccount, order.Remaining, cashAmount, s.penaltyBPS, plan.penalty)

		if plan.force {
			s.applyForce(day, order, plan, &result)
			continue
		}
		activeAfter = append(activeAfter, order)
	}

	for accountID, deltaBySecurity := range securityDelta {
		for security, delta := range deltaBySecurity {
			s.accounts[accountID].holdings[security] += delta
		}
	}
	for accountID, delta := range cashDelta {
		s.accounts[accountID].cash += delta
	}
	s.active = activeAfter
	return result
}

func (s *System) prepareCompensations(plans []plannedFill, referencePrices map[SecurityID]int64) error {
	for index := range plans {
		plan := &plans[index]
		if !plan.force {
			continue
		}
		order := plan.order
		referencePrice := referencePrices[order.Input.Security]
		plan.referencePrice = referencePrice
		originalValue := order.Input.Price * plan.remaining
		currentValue, overflow := multiply(referencePrice, plan.remaining)
		if overflow {
			return ErrInvalidParameter
		}
		if currentValue > originalValue {
			plan.compensation = currentValue - originalValue
		}
	}
	return nil
}

func (s *System) validateLedgerPostings(plans []plannedFill) error {
	type totals struct {
		penaltyPayable    *big.Int
		penaltyReceivable *big.Int
		compPayable       *big.Int
		compReceivable    *big.Int
	}
	values := make(map[AccountID]totals)

	for _, plan := range plans {
		if plan.penalty < 0 {
			return ErrInvalidParameter
		}
		order := plan.order
		if plan.remaining <= 0 {
			continue
		}

		responsible := order.Input.Buyer
		counterparty := order.Input.Seller
		if plan.responsible == ResponsibleSeller {
			responsible = order.Input.Seller
			counterparty = order.Input.Buyer
		}

		responsibleTotal := values[responsible]
		if responsibleTotal.penaltyPayable == nil {
			responsibleTotal.penaltyPayable = big.NewInt(s.accounts[responsible].penaltyPayable)
		}
		responsibleTotal.penaltyPayable.Add(responsibleTotal.penaltyPayable, big.NewInt(plan.penalty))
		values[responsible] = responsibleTotal

		counterpartyTotal := values[counterparty]
		if counterpartyTotal.penaltyReceivable == nil {
			counterpartyTotal.penaltyReceivable = big.NewInt(s.accounts[counterparty].penaltyReceivable)
		}
		counterpartyTotal.penaltyReceivable.Add(counterpartyTotal.penaltyReceivable, big.NewInt(plan.penalty))
		values[counterparty] = counterpartyTotal

		if plan.force && plan.responsible == ResponsibleSeller {
			sellerTotal := values[order.Input.Seller]
			if sellerTotal.compPayable == nil {
				sellerTotal.compPayable = big.NewInt(s.accounts[order.Input.Seller].compPayable)
			}
			sellerTotal.compPayable.Add(sellerTotal.compPayable, big.NewInt(plan.compensation))
			values[order.Input.Seller] = sellerTotal

			buyerTotal := values[order.Input.Buyer]
			if buyerTotal.compReceivable == nil {
				buyerTotal.compReceivable = big.NewInt(s.accounts[order.Input.Buyer].compReceivable)
			}
			buyerTotal.compReceivable.Add(buyerTotal.compReceivable, big.NewInt(plan.compensation))
			values[order.Input.Buyer] = buyerTotal
		}
	}

	for accountID, total := range values {
		account := s.accounts[accountID]
		if total.penaltyPayable != nil && (!total.penaltyPayable.IsInt64() || total.penaltyPayable.Int64() < account.penaltyPayable) {
			return ErrInvalidParameter
		}
		if total.penaltyReceivable != nil && (!total.penaltyReceivable.IsInt64() || total.penaltyReceivable.Int64() < account.penaltyReceivable) {
			return ErrInvalidParameter
		}
		if total.compPayable != nil && (!total.compPayable.IsInt64() || total.compPayable.Int64() < account.compPayable) {
			return ErrInvalidParameter
		}
		if total.compReceivable != nil && (!total.compReceivable.IsInt64() || total.compReceivable.Int64() < account.compReceivable) {
			return ErrInvalidParameter
		}
	}
	return nil
}

func (s *System) applyForce(day int, order *Order, plan plannedFill, result *ProcessResult) {
	force := ForceCloseResult{
		OrderID:            order.Input.ID,
		Remaining:          order.Remaining,
		Responsible:        plan.responsible,
		ReferencePrice:     plan.referencePrice,
		CompensationAmount: 0,
	}

	if plan.responsible == ResponsibleSeller {
		force.Payer = order.Input.Seller
		force.Payee = order.Input.Buyer
		force.CompensationAmount = plan.compensation
		s.accounts[order.Input.Seller].compPayable += plan.compensation
		s.accounts[order.Input.Buyer].compReceivable += plan.compensation
	}

	order.Remaining = 0
	order.Status = StatusForced
	result.ForceClosures = append(result.ForceClosures, force)
	s.log.Printf("force close day=%d order=%d failed_days=%d responsible=%s compensation=%d", day, order.Input.ID, order.FailedDays, plan.responsible, plan.compensation)
}

func (s *System) restoreDueDays(days []int) {
	for _, day := range days {
		heap.Push(&s.dueHeap, day)
	}
}

func (s *System) cleanupDay(day int, dueDays []int) {
	for _, dueDay := range dueDays {
		delete(s.byDue, dueDay)
	}
}

func ceilDiv(value, divisor int64) int64 {
	quotient, remainder := new(big.Int).QuoRem(big.NewInt(value), big.NewInt(divisor), new(big.Int))
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

func min64(values ...int64) int64 {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func multiply(a, b int64) (int64, bool) {
	product := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return product.Int64(), !product.IsInt64()
}
