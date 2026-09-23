package budget

import "errors"

var ErrLimitExceeded = errors.New("match step budget exceeded")

type Budget struct {
	steps int
	limit int
}

func New(limit int) *Budget {
	return &Budget{limit: limit}
}

func Unlimited() *Budget {
	return &Budget{limit: -1}
}

func (b *Budget) Add(amount int) error {
	if b == nil {
		return nil
	}
	b.steps += amount
	if b.limit >= 0 && b.steps > b.limit {
		return ErrLimitExceeded
	}
	return nil
}

func (b *Budget) Steps() int {
	if b == nil {
		return 0
	}
	return b.steps
}

func (b *Budget) Limit() int {
	if b == nil {
		return -1
	}
	return b.limit
}
