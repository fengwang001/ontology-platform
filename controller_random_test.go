package ontology

import (
	"flag"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

var verboseRandom = flag.Bool("verbose-random", false, "log every randomized operation and decision")

type randomOperation struct {
	name         string
	counterparty string
	instrument   string
	group        string
	amount       int64
	quantity     int64
	price        int64
	limit        int64
	lambda       int
}

type naiveModel struct {
	lambda       *big.Int
	parties      map[string]*naiveParty
	prices       map[string]int64
	groups       map[string]string
	instruments  []string
	nextSequence uint64
}

type naiveParty struct {
	limit      *big.Int
	collateral *big.Int
	positions  map[string]int64
	breached   bool
	sequence   uint64
}

func newNaiveModel(lambda int) *naiveModel {
	return &naiveModel{
		lambda:      big.NewInt(int64(lambda)),
		parties:     make(map[string]*naiveParty),
		prices:      make(map[string]int64),
		groups:      make(map[string]string),
		instruments: nil,
	}
}

func TestRandomizedAgainstNaiveBigIntModel(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		random := rand.New(rand.NewSource(seed))
		lambda := []int{0, 1000, 10000}[random.Intn(3)]
		controller, err := New(lambda)
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		model := newNaiveModel(lambda)

		for step := 0; step < 80; step++ {
			operation := randomOperation{
				name:         []string{"register", "price", "group", "trade", "post", "release"}[random.Intn(6)],
				counterparty: string(rune('A' + random.Intn(4))),
				instrument:   string(rune('W' + random.Intn(5))),
				group:        string(rune('g' + random.Intn(3))),
			}
			operation.amount = int64(random.Intn(121)) - 10
			operation.quantity = int64(random.Intn(21)) - 10
			operation.price = int64(random.Intn(20)) + 1
			operation.limit = int64(random.Intn(500))
			operation.lambda = lambda

			got := applyOperation(controller, operation)
			want, reason := model.apply(operation)
			if errorName(got) != errorName(want) {
				t.Fatalf("seed %d step %d operation %+v: got %v, want %v (%s)",
					seed, step, operation, got, want, reason)
			}
			if *verboseRandom {
				t.Logf("seed=%d step=%d op=%+v result=%q model_result=%q reason=%s",
					seed, step, operation, errorName(got), errorName(want), reason)
			}

			model.assertMatches(t, controller, seed, step)
		}
	}
}

func applyOperation(controller *Controller, operation randomOperation) error {
	switch operation.name {
	case "register":
		return controller.Register(operation.counterparty, operation.limit)
	case "price":
		return controller.SetPrice(operation.instrument, operation.price)
	case "group":
		return controller.SetGroup(operation.instrument, operation.group)
	case "trade":
		return controller.Trade(operation.counterparty, operation.instrument, operation.quantity)
	case "post":
		return controller.Post(operation.counterparty, operation.amount)
	case "release":
		return controller.Release(operation.counterparty, operation.amount)
	default:
		panic("unknown operation")
	}
}

func (m *naiveModel) apply(operation randomOperation) (error, string) {
	switch operation.name {
	case "register":
		if operation.counterparty == "" || operation.limit < 0 || operation.limit > maxLimit {
			return ErrInvalidArgument, "invalid register parameters"
		}
		if _, exists := m.parties[operation.counterparty]; exists {
			return ErrCounterpartyAlreadyRegistered, "duplicate counterparty"
		}
		m.parties[operation.counterparty] = &naiveParty{
			limit:      big.NewInt(operation.limit),
			collateral: big.NewInt(0),
			positions:  make(map[string]int64),
		}
		m.audit()
		return nil, "accepted register"
	case "price":
		if operation.instrument == "" || operation.price < 1 || operation.price > maxPrice {
			return ErrInvalidArgument, "invalid price parameters"
		}
		if _, exists := m.prices[operation.instrument]; !exists && len(m.instruments) >= maxInstruments {
			return ErrInvalidArgument, "instrument capacity exceeded"
		}
		if _, exists := m.prices[operation.instrument]; !exists {
			m.instruments = append(m.instruments, operation.instrument)
		}
		m.prices[operation.instrument] = operation.price
		m.audit()
		return nil, "accepted price"
	case "group":
		if operation.instrument == "" || operation.group == "" {
			return ErrInvalidArgument, "invalid group parameters"
		}
		if _, exists := m.prices[operation.instrument]; !exists {
			return ErrNoPrice, "instrument has no price"
		}
		m.groups[operation.instrument] = operation.group
		m.audit()
		return nil, "accepted group"
	case "trade":
		if operation.counterparty == "" || operation.instrument == "" ||
			operation.quantity == 0 || operation.quantity < -maxQuantity || operation.quantity > maxQuantity {
			return ErrInvalidArgument, "invalid trade parameters"
		}
		party, exists := m.parties[operation.counterparty]
		if !exists {
			return ErrCounterpartyNotFound, "counterparty absent"
		}
		price, exists := m.prices[operation.instrument]
		if !exists {
			return ErrNoPrice, "instrument has no price"
		}
		nextQuantity := party.positions[operation.instrument] + operation.quantity
		if nextQuantity < -maxQuantity || nextQuantity > maxQuantity {
			return ErrInvalidArgument, "resulting position exceeds bound"
		}
		before := m.exposure(party)
		party.positions[operation.instrument] = nextQuantity
		after := m.exposure(party)
		if after.Cmp(party.limit) > 0 && after.Cmp(before) > 0 {
			party.positions[operation.instrument] = nextQuantity - operation.quantity
			return ErrLimitExceeded, "trade increases exposure beyond limit: before " + before.String() + ", after " + after.String() + ", price " + big.NewInt(price).String()
		}
		m.audit()
		return nil, "accepted trade: exposure " + before.String() + " -> " + after.String()
	case "post":
		if operation.counterparty == "" || operation.amount < 1 || operation.amount > maxPostSize {
			return ErrInvalidArgument, "invalid post parameters"
		}
		party, exists := m.parties[operation.counterparty]
		if !exists {
			return ErrCounterpartyNotFound, "counterparty absent"
		}
		if new(big.Int).Add(party.collateral, big.NewInt(operation.amount)).Cmp(big.NewInt(maxCollateral)) > 0 {
			return ErrInvalidArgument, "collateral cap exceeded"
		}
		party.collateral.Add(party.collateral, big.NewInt(operation.amount))
		m.audit()
		return nil, "accepted post"
	case "release":
		if operation.counterparty == "" || operation.amount < 1 {
			return ErrInvalidArgument, "invalid release parameters"
		}
		party, exists := m.parties[operation.counterparty]
		if !exists {
			return ErrCounterpartyNotFound, "counterparty absent"
		}
		amount := big.NewInt(operation.amount)
		if amount.Cmp(party.collateral) > 0 {
			return ErrInsufficientCollateral, "collateral insufficient"
		}
		before := m.exposure(party)
		party.collateral.Sub(party.collateral, amount)
		after := m.exposure(party)
		if after.Cmp(party.limit) > 0 && after.Cmp(before) > 0 {
			party.collateral.Add(party.collateral, amount)
			return ErrLimitExceeded, "release increases exposure beyond limit: before " + before.String() + ", after " + after.String()
		}
		m.audit()
		return nil, "accepted release: exposure " + before.String() + " -> " + after.String()
	default:
		panic("unknown operation")
	}
}

func (m *naiveModel) exposure(party *naiveParty) *big.Int {
	groupLong := make(map[string]*big.Int)
	groupShort := make(map[string]*big.Int)
	marked := new(big.Int)

	for instrumentName, quantity := range party.positions {
		if quantity == 0 {
			continue
		}
		price := big.NewInt(m.prices[instrumentName])
		value := new(big.Int).Mul(big.NewInt(quantity), price)
		absoluteValue := new(big.Int).Abs(value)
		marked.Add(marked, value)

		groupName := m.groups[instrumentName]
		if groupName == "" {
			groupName = instrumentName
		}
		if groupLong[groupName] == nil {
			groupLong[groupName] = new(big.Int)
			groupShort[groupName] = new(big.Int)
		}
		if quantity > 0 {
			groupLong[groupName].Add(groupLong[groupName], absoluteValue)
		} else {
			groupShort[groupName].Add(groupShort[groupName], absoluteValue)
		}
	}

	buffer := new(big.Int)
	for groupName := range groupLong {
		hedged := new(big.Int).Set(groupLong[groupName])
		if groupShort[groupName].Cmp(hedged) < 0 {
			hedged.Set(groupShort[groupName])
		}
		term := new(big.Int).Mul(hedged, m.lambda)
		term.Div(term, big.NewInt(int64(rateDenominator)))
		if new(big.Int).Mul(term, big.NewInt(int64(rateDenominator))).Cmp(
			new(big.Int).Mul(hedged, m.lambda)) < 0 {
			term.Add(term, big.NewInt(1))
		}
		buffer.Add(buffer, term)
	}

	exposure := new(big.Int).Add(marked, buffer)
	exposure.Sub(exposure, party.collateral)
	if exposure.Sign() < 0 {
		return new(big.Int)
	}
	return exposure
}

func (m *naiveModel) audit() {
	names := make([]string, 0, len(m.parties))
	for name := range m.parties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		party := m.parties[name]
		exposure := m.exposure(party)
		if exposure.Cmp(party.limit) > 0 {
			if !party.breached {
				m.nextSequence++
				party.breached = true
				party.sequence = m.nextSequence
			}
		} else if party.breached {
			party.breached = false
			party.sequence = 0
		}
	}
}

func (m *naiveModel) assertMatches(t *testing.T, controller *Controller, seed int64, step int) {
	t.Helper()

	for name, party := range m.parties {
		gotExposure, err := controller.Exposure(name)
		if err != nil {
			t.Fatalf("seed %d step %d Exposure(%s): %v", seed, step, name, err)
		}
		wantExposure := m.exposure(party)
		if wantExposure.Cmp(big.NewInt(gotExposure)) != 0 {
			t.Fatalf("seed %d step %d exposure for %s: got %d, want %s",
				seed, step, name, gotExposure, wantExposure.String())
		}
	}

	gotBreaches := controller.Breaches()
	wantBreaches := make([]Breach, 0)
	for name, party := range m.parties {
		if party.breached {
			wantBreaches = append(wantBreaches, Breach{
				Counterparty: name,
				Exposure:     m.exposure(party).Int64(),
			})
		}
	}
	sort.Slice(wantBreaches, func(i, j int) bool {
		return m.parties[wantBreaches[i].Counterparty].sequence <
			m.parties[wantBreaches[j].Counterparty].sequence
	})
	if len(gotBreaches) != len(wantBreaches) {
		t.Fatalf("seed %d step %d breach count: got %+v, want %+v",
			seed, step, gotBreaches, wantBreaches)
	}
	for i := range wantBreaches {
		if gotBreaches[i] != wantBreaches[i] {
			t.Fatalf("seed %d step %d breach %d: got %+v, want %+v",
				seed, step, i, gotBreaches[i], wantBreaches[i])
		}
	}
}

func errorName(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
