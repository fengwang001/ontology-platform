package ontology

import (
	"errors"
	"math/bits"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument               = errors.New("invalid argument")
	ErrCounterpartyAlreadyRegistered = errors.New("counterparty already registered")
	ErrCounterpartyNotFound          = errors.New("counterparty not found")
	ErrNoPrice                       = errors.New("instrument has no price")
	ErrInsufficientCollateral        = errors.New("insufficient collateral")
	ErrLimitExceeded                 = errors.New("limit exceeded")
)

const (
	maxInstruments         = 1000
	maxQuantity            = 1_000_000
	maxPrice               = 1_000_000
	maxCollateral          = 1_000_000_000_000_000
	maxPostSize            = 1_000_000_000_000
	maxLimit               = maxCollateral
	rateDenominator uint64 = 10_000
)

type Breach struct {
	Counterparty string
	Exposure     int64
}

type Controller struct {
	mu sync.RWMutex

	lambda         uint64
	instruments    map[string]*instrumentRecord
	counterparties map[string]*party
	nextSequence   uint64
}

type instrumentRecord struct {
	price int64
	group string
}

type party struct {
	limit      int64
	collateral int64
	positions  map[string]int64
	breached   bool
	sequence   uint64
}

func New(lambda int) (*Controller, error) {
	if lambda < 0 || lambda > int(rateDenominator) {
		return nil, ErrInvalidArgument
	}

	return &Controller{
		lambda:         uint64(lambda),
		instruments:    make(map[string]*instrumentRecord),
		counterparties: make(map[string]*party),
	}, nil
}

func (c *Controller) Register(counterparty string, limit int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if counterparty == "" {
		return ErrInvalidArgument
	}
	if _, exists := c.counterparties[counterparty]; exists {
		return ErrCounterpartyAlreadyRegistered
	}
	if limit < 0 || limit > maxLimit {
		return ErrInvalidArgument
	}

	c.counterparties[counterparty] = &party{
		limit:     limit,
		positions: make(map[string]int64),
	}
	c.auditLocked()
	return nil
}

func (c *Controller) SetPrice(instrument string, price int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if instrument == "" || price < 1 || price > maxPrice {
		return ErrInvalidArgument
	}
	if _, exists := c.instruments[instrument]; !exists && len(c.instruments) >= maxInstruments {
		return ErrInvalidArgument
	}

	if current, exists := c.instruments[instrument]; exists {
		current.price = price
	} else {
		c.instruments[instrument] = &instrumentRecord{price: price}
	}

	c.auditLocked()
	return nil
}

func (c *Controller) SetGroup(instrument, group string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if instrument == "" || group == "" {
		return ErrInvalidArgument
	}
	current, exists := c.instruments[instrument]
	if !exists {
		return ErrNoPrice
	}

	current.group = group
	c.auditLocked()
	return nil
}

func (c *Controller) Trade(counterparty, instrument string, quantity int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if counterparty == "" || instrument == "" || quantity == 0 ||
		quantity < -maxQuantity || quantity > maxQuantity {
		return ErrInvalidArgument
	}
	cp, exists := c.counterparties[counterparty]
	if !exists {
		return ErrCounterpartyNotFound
	}
	if _, exists := c.instruments[instrument]; !exists {
		return ErrNoPrice
	}

	currentQuantity := cp.positions[instrument]
	nextQuantity := currentQuantity + quantity
	if nextQuantity < -maxQuantity || nextQuantity > maxQuantity {
		return ErrInvalidArgument
	}

	beforeExposure := c.exposureLocked(cp)
	cp.positions[instrument] = nextQuantity
	nextExposure := c.exposureLocked(cp)
	if nextExposure > cp.limit && nextExposure > beforeExposure {
		cp.positions[instrument] = currentQuantity
		return ErrLimitExceeded
	}

	c.auditLocked()
	return nil
}

func (c *Controller) Post(counterparty string, amount int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if counterparty == "" || amount < 1 || amount > maxPostSize {
		return ErrInvalidArgument
	}
	cp, exists := c.counterparties[counterparty]
	if !exists {
		return ErrCounterpartyNotFound
	}
	if cp.collateral > maxCollateral-amount {
		return ErrInvalidArgument
	}

	cp.collateral += amount
	c.auditLocked()
	return nil
}

func (c *Controller) Release(counterparty string, amount int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if counterparty == "" || amount < 1 {
		return ErrInvalidArgument
	}
	cp, exists := c.counterparties[counterparty]
	if !exists {
		return ErrCounterpartyNotFound
	}
	if amount > cp.collateral {
		return ErrInsufficientCollateral
	}

	beforeExposure := c.exposureLocked(cp)
	cp.collateral -= amount
	nextExposure := c.exposureLocked(cp)
	if nextExposure > cp.limit && nextExposure > beforeExposure {
		cp.collateral += amount
		return ErrLimitExceeded
	}

	c.auditLocked()
	return nil
}

func (c *Controller) Exposure(counterparty string) (int64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if counterparty == "" {
		return 0, ErrInvalidArgument
	}
	cp, exists := c.counterparties[counterparty]
	if !exists {
		return 0, ErrCounterpartyNotFound
	}
	return c.exposureLocked(cp), nil
}

func (c *Controller) Breaches() []Breach {
	c.mu.RLock()
	defer c.mu.RUnlock()

	breaches := make([]Breach, 0)
	for name, cp := range c.counterparties {
		if cp.breached {
			breaches = append(breaches, Breach{
				Counterparty: name,
				Exposure:     c.exposureLocked(cp),
			})
		}
	}
	sort.Slice(breaches, func(i, j int) bool {
		return c.counterparties[breaches[i].Counterparty].sequence <
			c.counterparties[breaches[j].Counterparty].sequence
	})
	return breaches
}

type groupSide struct {
	long  uint64
	short uint64
}

func (c *Controller) exposureLocked(cp *party) int64 {
	groups := make(map[string]groupSide)
	var markedValue int64

	for instrumentName, quantity := range cp.positions {
		if quantity == 0 {
			continue
		}
		inst := c.instruments[instrumentName]
		value := uint64(0)
		if quantity > 0 {
			value = uint64(quantity) * uint64(inst.price)
			markedValue += int64(value)
		} else {
			value = uint64(-quantity) * uint64(inst.price)
			markedValue -= int64(value)
		}

		groupName := inst.group
		if groupName == "" {
			groupName = instrumentName
		}
		side := groups[groupName]
		if quantity > 0 {
			side.long += value
		} else {
			side.short += value
		}
		groups[groupName] = side
	}

	var buffer uint64
	for _, side := range groups {
		hedged := side.long
		if side.short < hedged {
			hedged = side.short
		}
		buffer += ceilRate(hedged, c.lambda)
	}

	adjustedValue := markedValue + int64(buffer)
	exposure := adjustedValue - cp.collateral
	if exposure < 0 {
		return 0
	}
	return exposure
}

func ceilRate(value uint64, rate uint64) uint64 {
	high, low := bits.Mul64(value, rate)
	quotient, remainder := bits.Div64(high, low, rateDenominator)
	if remainder != 0 {
		quotient++
	}
	return quotient
}

func (c *Controller) auditLocked() {
	names := make([]string, 0, len(c.counterparties))
	for name := range c.counterparties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cp := c.counterparties[name]
		exposure := c.exposureLocked(cp)
		if exposure > cp.limit {
			if !cp.breached {
				c.nextSequence++
				cp.breached = true
				cp.sequence = c.nextSequence
			}
			continue
		}
		if cp.breached {
			cp.breached = false
			cp.sequence = 0
		}
	}
}
