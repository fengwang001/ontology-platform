package rating

import (
	"errors"
	"sync"
)

var ErrInvalidArgument = errors.New("invalid argument")

type Catalog struct {
	mu     sync.Mutex
	rates  map[int]int64
	maxNow int64
}

func New() *Catalog {
	return &Catalog{rates: make(map[int]int64)}
}

func (c *Catalog) SetRate(rateGroup int, price int64, now int64) error {
	if rateGroup < 1 || rateGroup > 1000 || price < 1 || price > 1_000_000 || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.maxNow {
		return errors.New("clock moved backwards")
	}
	c.maxNow = now
	c.rates[rateGroup] = price
	return nil
}

func (c *Catalog) Price(rateGroup int) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	price, ok := c.rates[rateGroup]
	return price, ok
}
