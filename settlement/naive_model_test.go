package settlement

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

type naiveAccount struct {
	cash              int64
	holdings          map[SecurityID]int64
	penaltyPayable    int64
	penaltyReceivable int64
	compPayable       int64
	compReceivable    int64
}

type naiveOrder struct {
	input      OrderInput
	delivered  int64
	remaining  int64
	failedDays int
	status     OrderStatus
}

type naiveModel struct {
	accounts map[AccountID]*naiveAccount
	orders   map[uint64]*naiveOrder
	lastDay  int
}

func newNaiveModel(accounts []Account) *naiveModel {
	model := &naiveModel{
		accounts: make(map[AccountID]*naiveAccount, len(accounts)),
		orders:   make(map[uint64]*naiveOrder),
		lastDay:  math.MinInt,
	}
	for _, account := range accounts {
		holdings := make(map[SecurityID]int64, len(account.Holdings))
		for security, quantity := range account.Holdings {
			holdings[security] = quantity
		}
		model.accounts[account.ID] = &naiveAccount{
			cash:     account.Cash,
			holdings: holdings,
		}
	}
	return model
}

func (model *naiveModel) register(input OrderInput) {
	model.orders[input.ID] = &naiveOrder{
		input:     input,
		remaining: input.Quantity,
		status:    StatusPending,
	}
}

func (model *naiveModel) process(day int, prices map[SecurityID]int64, maxFailDays int, penaltyBPS int64) {
	ids := make([]uint64, 0, len(model.orders))
	for id, order := range model.orders {
		if order.remaining > 0 && order.input.SettlementDay <= day {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	securityReserved := make(map[AccountID]map[SecurityID]int64)
	cashReserved := make(map[AccountID]int64)
	securityDelta := make(map[AccountID]map[SecurityID]int64)
	cashDelta := make(map[AccountID]int64)

	for _, id := range ids {
		order := model.orders[id]
		seller := model.accounts[order.input.Seller]
		buyer := model.accounts[order.input.Buyer]
		reservedSecurities := int64(0)
		if reservedBySeller := securityReserved[order.input.Seller]; reservedBySeller != nil {
			reservedSecurities = reservedBySeller[order.input.Security]
		}
		sellerAvailable := seller.holdings[order.input.Security] - reservedSecurities
		buyerAffordable := (buyer.cash - cashReserved[order.input.Buyer]) / order.input.Price
		requested := order.remaining
		delivered := min64(order.remaining, sellerAvailable, buyerAffordable)
		if !order.input.AllowPartial && delivered < order.remaining {
			delivered = 0
		}

		if delivered > 0 {
			amount := delivered * order.input.Price
			if securityReserved[order.input.Seller] == nil {
				securityReserved[order.input.Seller] = make(map[SecurityID]int64)
			}
			securityReserved[order.input.Seller][order.input.Security] += delivered
			cashReserved[order.input.Buyer] += amount
			if securityDelta[order.input.Seller] == nil {
				securityDelta[order.input.Seller] = make(map[SecurityID]int64)
			}
			if securityDelta[order.input.Buyer] == nil {
				securityDelta[order.input.Buyer] = make(map[SecurityID]int64)
			}
			securityDelta[order.input.Seller][order.input.Security] -= delivered
			securityDelta[order.input.Buyer][order.input.Security] += delivered
			cashDelta[order.input.Seller] += amount
			cashDelta[order.input.Buyer] -= amount
			order.delivered += delivered
			order.remaining -= delivered
			if order.remaining == 0 {
				order.status = StatusCompleted
			} else {
				order.status = StatusPartial
			}
		}

		if order.remaining == 0 {
			continue
		}

		if order.delivered == 0 {
			order.status = StatusPending
		}
		order.failedDays++
		responsibleAccount := buyer
		counterparty := seller
		responsibleParty := ResponsibleBuyer
		if sellerAvailable < requested {
			responsibleAccount = seller
			counterparty = buyer
			responsibleParty = ResponsibleSeller
		}
		cashAmount := order.remaining * order.input.Price
		penalty := ceilDiv(cashAmount*penaltyBPS, 10000)
		responsibleAccount.penaltyPayable += penalty
		counterparty.penaltyReceivable += penalty

		if order.failedDays == maxFailDays {
			order.status = StatusForced
			if responsibleParty == ResponsibleSeller {
				currentValue := prices[order.input.Security] * order.remaining
				originalValue := order.input.Price * order.remaining
				if currentValue > originalValue {
					seller.compPayable += currentValue - originalValue
					buyer.compReceivable += currentValue - originalValue
				}
			}
			order.remaining = 0
		}
	}
	for accountID, deltaBySecurity := range securityDelta {
		for security, delta := range deltaBySecurity {
			model.accounts[accountID].holdings[security] += delta
		}
	}
	for accountID, delta := range cashDelta {
		model.accounts[accountID].cash += delta
	}
	model.lastDay = day
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}

	const days = 12
	businessDays := make([]int, days)
	for index := range businessDays {
		businessDays[index] = index + 1
	}

	for seed := int64(0); seed < 200; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			accounts := []Account{
				{ID: "a", Cash: int64(random.Intn(80) + 1)},
				{ID: "b", Cash: int64(random.Intn(80) + 1), Holdings: map[SecurityID]int64{"X": int64(random.Intn(8) + 1), "Y": int64(random.Intn(8) + 1)}},
				{ID: "c", Cash: int64(random.Intn(80) + 1), Holdings: map[SecurityID]int64{"X": int64(random.Intn(8) + 1)}},
				{ID: "d", Cash: int64(random.Intn(80) + 1)},
			}
			system := newTestSystem(t, Config{BusinessDays: businessDays, MaxFailDays: 1 + random.Intn(4), PenaltyBPS: int64(random.Intn(300))}, accounts)
			model := newNaiveModel(accounts)

			nextID := uint64(1)
			for day := 1; day <= days; day++ {
				registrations := random.Intn(5)
				for count := 0; count < registrations; count++ {
					buyer := accounts[random.Intn(len(accounts))].ID
					seller := accounts[random.Intn(len(accounts))].ID
					input := OrderInput{
						ID:            nextID,
						Security:      []SecurityID{"X", "Y"}[random.Intn(2)],
						Buyer:         buyer,
						Seller:        seller,
						Quantity:      int64(random.Intn(10) + 1),
						Price:         int64(random.Intn(10) + 1),
						SettlementDay: day + random.Intn(3),
						AllowPartial:  random.Intn(2) == 0,
					}
					if input.SettlementDay > days {
						input.SettlementDay = days
					}
					err := system.RegisterOrder(input)
					if err == nil {
						model.register(input)
						nextID++
					}
				}

				prices := map[SecurityID]int64{"X": int64(random.Intn(12) + 1), "Y": int64(random.Intn(12) + 1)}
				_, err := system.ProcessBusinessDay(day, prices)
				if err != nil {
					t.Fatalf("seed=%d day=%d unexpected process error: %v", seed, day, err)
				}
				model.process(day, prices, system.maxFailDays, system.penaltyBPS)
				assertNaiveEquivalence(t, system, model)
			}
		})
	}
}

func assertNaiveEquivalence(t *testing.T, system *System, model *naiveModel) {
	t.Helper()
	for accountID, expected := range model.accounts {
		position, err := system.Position(accountID)
		if err != nil {
			t.Fatalf("Position %s: %v", accountID, err)
		}
		if position.Cash != expected.cash {
			t.Fatalf("account %s cash = %d, want %d", accountID, position.Cash, expected.cash)
		}
		for security, quantity := range expected.holdings {
			if position.Holdings[security] != quantity {
				t.Fatalf("account %s security %s = %d, want %d", accountID, security, position.Holdings[security], quantity)
			}
		}
		penalties, err := system.PenaltyBalances(accountID)
		if err != nil {
			t.Fatalf("PenaltyBalances %s: %v", accountID, err)
		}
		if penalties.Payable != expected.penaltyPayable || penalties.Receivable != expected.penaltyReceivable {
			t.Fatalf("account %s penalties = %+v, want payable %d receivable %d", accountID, penalties, expected.penaltyPayable, expected.penaltyReceivable)
		}
		compensation, err := system.CompensationBalances(accountID)
		if err != nil {
			t.Fatalf("CompensationBalances %s: %v", accountID, err)
		}
		if compensation.Payable != expected.compPayable || compensation.Receivable != expected.compReceivable {
			t.Fatalf("account %s compensation = %+v, want payable %d receivable %d", accountID, compensation, expected.compPayable, expected.compReceivable)
		}
	}

	for id, expected := range model.orders {
		actual, err := system.Order(id)
		if err != nil {
			t.Fatalf("Order %d: %v", id, err)
		}
		if actual.Delivered != expected.delivered || actual.Remaining != expected.remaining || actual.Status != expected.status || actual.FailedDays != expected.failedDays {
			t.Fatalf("order %d = %+v, want delivered %d remaining %d status %s failed %d", id, actual, expected.delivered, expected.remaining, expected.status, expected.failedDays)
		}
	}
}
