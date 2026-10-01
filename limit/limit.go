// Package limit implements a counterparty net-exposure limit controller.
//
// The controller aggregates signed net positions per instrument for each
// counterparty, values them at mark prices, credits a basis buffer for the
// hedged (long/short offsetting) part inside each instrument group, and
// checks the resulting exposure against a per-counterparty limit before
// accepting trades and collateral releases.
//
// All methods are safe for concurrent use; every accepted operation
// atomically re-evaluates the breach marks of all counterparties.
package limit

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

// Rejection reasons returned by the controller operations. Each operation
// reports only the first applicable reason, in the order the sentinels are
// declared relevant for that operation (see the method documentation).
var (
	// ErrInvalidParam indicates an out-of-range or otherwise illegal
	// parameter (empty strings, out-of-range quantities, instrument count
	// overflow, position or collateral bound violations).
	ErrInvalidParam = errors.New("limit: invalid parameter")
	// ErrDuplicateCounterparty indicates Register was called for an
	// already registered counterparty.
	ErrDuplicateCounterparty = errors.New("limit: counterparty already registered")
	// ErrNotRegistered indicates the counterparty is not registered.
	ErrNotRegistered = errors.New("limit: counterparty not registered")
	// ErrNoPrice indicates the instrument has no mark price.
	ErrNoPrice = errors.New("limit: instrument has no price")
	// ErrInsufficientCollateral indicates a Release amount exceeds the
	// posted collateral.
	ErrInsufficientCollateral = errors.New("limit: insufficient collateral")
	// ErrLimitExceeded indicates the operation would increase exposure
	// beyond the counterparty limit.
	ErrLimitExceeded = errors.New("limit: exposure limit exceeded")
)

const (
	maxLambda      = 10000
	maxLimit       = int64(1_000_000_000_000_000) // 10^15
	maxInstruments = 1000
	maxPrice       = int64(1_000_000)
	maxPosition    = int64(1_000_000)
	maxPost        = int64(1_000_000_000_000) // 10^12
	maxCollateral  = int64(1_000_000_000_000_000)
)

var bigDenom = big.NewInt(10000)

type instrument struct {
	price int64
	group string
}

type counterparty struct {
	limit      int64
	positions  map[string]int64
	collateral int64
	marked     bool
	seq        uint64
}

// Breach describes one marked counterparty: its name, current exposure and
// the global sequence number assigned when it entered the breach state.
type Breach struct {
	Counterparty []byte
	Exposure     *big.Int
	Seq          uint64
}

// Controller is a counterparty net-exposure limit controller.
type Controller struct {
	mu          sync.Mutex
	lambda      int64
	instruments map[string]*instrument
	parties     map[string]*counterparty
	seqCounter  uint64
}

// New creates a controller with basis buffer rate lambda, expressed in
// ten-thousandths (0 to 10000). It returns ErrInvalidParam otherwise.
func New(lambda int64) (*Controller, error) {
	if lambda < 0 || lambda > maxLambda {
		return nil, ErrInvalidParam
	}
	return &Controller{
		lambda:      lambda,
		instruments: make(map[string]*instrument),
		parties:     make(map[string]*counterparty),
	}, nil
}

// Register registers a counterparty with exposure limit l (0 to 10^15).
// cp must be non-empty. Reasons, in order: ErrInvalidParam,
// ErrDuplicateCounterparty.
func (c *Controller) Register(cp []byte, l int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(cp) == 0 || l < 0 || l > maxLimit {
		return ErrInvalidParam
	}
	name := string(cp)
	if _, ok := c.parties[name]; ok {
		return ErrDuplicateCounterparty
	}
	c.parties[name] = &counterparty{limit: l, positions: make(map[string]int64)}
	c.recheck()
	return nil
}

// SetPrice sets or updates the mark price px (1 to 10^6) of an instrument.
// At most 1000 distinct instruments may be priced. No limit check is
// performed. Reasons: ErrInvalidParam.
func (c *Controller) SetPrice(inst []byte, px int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(inst) == 0 || px < 1 || px > maxPrice {
		return ErrInvalidParam
	}
	name := string(inst)
	if ins, ok := c.instruments[name]; ok {
		ins.price = px
	} else {
		if len(c.instruments) >= maxInstruments {
			return ErrInvalidParam
		}
		c.instruments[name] = &instrument{price: px, group: name}
	}
	c.recheck()
	return nil
}

// SetGroup assigns a priced instrument to group g (non-empty), overriding
// any previous assignment. Instruments never assigned form singleton groups
// named after themselves. No limit check is performed. Reasons, in order:
// ErrInvalidParam, ErrNoPrice.
func (c *Controller) SetGroup(inst []byte, g []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(inst) == 0 || len(g) == 0 {
		return ErrInvalidParam
	}
	ins, ok := c.instruments[string(inst)]
	if !ok {
		return ErrNoPrice
	}
	ins.group = string(g)
	c.recheck()
	return nil
}

// Trade executes a trade of n units (non-zero, |n| <= 10^6) of a priced
// instrument for a registered counterparty. After the trade the instrument
// position must satisfy |q| <= 10^6. The trade is rejected with
// ErrLimitExceeded if the resulting exposure both exceeds the limit and
// increases relative to the current exposure. Reasons, in order:
// ErrInvalidParam, ErrNotRegistered, ErrNoPrice, ErrLimitExceeded.
func (c *Controller) Trade(cp []byte, inst []byte, n int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(cp) == 0 || len(inst) == 0 || n == 0 || n > maxPosition || n < -maxPosition {
		return ErrInvalidParam
	}
	p, ok := c.parties[string(cp)]
	if !ok {
		return ErrNotRegistered
	}
	name := string(inst)
	if _, ok := c.instruments[name]; !ok {
		return ErrNoPrice
	}
	old := p.positions[name]
	next := old + n
	if next > maxPosition || next < -maxPosition {
		return ErrInvalidParam
	}
	before := c.exposure(p)
	p.positions[name] = next
	after := c.exposure(p)
	if after.Cmp(big.NewInt(p.limit)) <= 0 || after.Cmp(before) <= 0 {
		c.recheck()
		return nil
	}
	p.positions[name] = old
	return ErrLimitExceeded
}

// Post deposits collateral g (1 to 10^12) for a registered counterparty.
// The total collateral must not exceed 10^15. No limit check is performed.
// Reasons, in order: ErrInvalidParam, ErrNotRegistered.
func (c *Controller) Post(cp []byte, g int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(cp) == 0 || g < 1 || g > maxPost {
		return ErrInvalidParam
	}
	p, ok := c.parties[string(cp)]
	if !ok {
		return ErrNotRegistered
	}
	if p.collateral+g > maxCollateral {
		return ErrInvalidParam
	}
	p.collateral += g
	c.recheck()
	return nil
}

// Release returns collateral g (1 <= g <= G) to a registered counterparty.
// It is rejected with ErrLimitExceeded if the resulting exposure both
// exceeds the limit and increases relative to the current exposure.
// Reasons, in order: ErrInvalidParam, ErrNotRegistered,
// ErrInsufficientCollateral, ErrLimitExceeded.
func (c *Controller) Release(cp []byte, g int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(cp) == 0 || g < 1 {
		return ErrInvalidParam
	}
	p, ok := c.parties[string(cp)]
	if !ok {
		return ErrNotRegistered
	}
	if g > p.collateral {
		return ErrInsufficientCollateral
	}
	before := c.exposure(p)
	p.collateral -= g
	after := c.exposure(p)
	if after.Cmp(big.NewInt(p.limit)) <= 0 || after.Cmp(before) <= 0 {
		c.recheck()
		return nil
	}
	p.collateral += g
	return ErrLimitExceeded
}

// Exposure returns the current exposure of a registered counterparty.
func (c *Controller) Exposure(cp []byte) (*big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.parties[string(cp)]
	if !ok {
		return nil, ErrNotRegistered
	}
	return c.exposure(p), nil
}

// Breaches returns the currently marked counterparties with their current
// exposures, ordered by ascending mark sequence number (earliest breach
// first).
func (c *Controller) Breaches() []Breach {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Breach
	for name, p := range c.parties {
		if !p.marked {
			continue
		}
		out = append(out, Breach{
			Counterparty: []byte(name),
			Exposure:     c.exposure(p),
			Seq:          p.seq,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// exposure computes E = max(0, W' - G) where W' = W + sum over groups of
// ceil(lambda*H_g/10000), each group rounded up individually. All
// intermediate products use big.Int because lambda*H_g may exceed 64 bits.
func (c *Controller) exposure(p *counterparty) *big.Int {
	w := new(big.Int)
	longs := make(map[string]*big.Int)
	shorts := make(map[string]*big.Int)
	for name, q := range p.positions {
		if q == 0 {
			continue
		}
		ins := c.instruments[name]
		v := new(big.Int).Mul(big.NewInt(q), big.NewInt(ins.price))
		w.Add(w, v)
		if q > 0 {
			addBig(longs, ins.group, v)
		} else {
			addBig(shorts, ins.group, new(big.Int).Neg(v))
		}
	}
	wPrime := new(big.Int).Set(w)
	for group, long := range longs {
		short := shorts[group]
		if short == nil {
			continue
		}
		h := long
		if short.Cmp(long) < 0 {
			h = short
		}
		// buffer = ceil(lambda * H / 10000), H >= 0.
		buf := new(big.Int).Mul(big.NewInt(c.lambda), h)
		buf.Add(buf, big.NewInt(9999))
		buf.Div(buf, bigDenom)
		wPrime.Add(wPrime, buf)
	}
	e := wPrime.Sub(wPrime, big.NewInt(p.collateral))
	if e.Sign() < 0 {
		e.SetInt64(0)
	}
	return e
}

func addBig(m map[string]*big.Int, key string, v *big.Int) {
	if s, ok := m[key]; ok {
		s.Add(s, v)
	} else {
		m[key] = new(big.Int).Set(v)
	}
}

// recheck re-evaluates all counterparties in ascending byte order of their
// names. Newly breaching counterparties are marked with the next global
// sequence number; counterparties no longer breaching are unmarked.
func (c *Controller) recheck() {
	names := make([]string, 0, len(c.parties))
	for name := range c.parties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := c.parties[name]
		over := c.exposure(p).Cmp(big.NewInt(p.limit)) > 0
		switch {
		case over && !p.marked:
			c.seqCounter++
			p.marked = true
			p.seq = c.seqCounter
		case !over && p.marked:
			p.marked = false
			p.seq = 0
		}
	}
}
