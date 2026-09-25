package task

import (
	"errors"
	"time"
)

const (
	MaxCost   = 1_000_000_000_000.0
	MinWeight = 1e-12
	MaxWeight = 1_000_000_000_000.0
)

var (
	ErrEmptyTenant = errors.New("task: empty tenant id")
	ErrBadCost     = errors.New("task: cost out of range")
	ErrBadWeight   = errors.New("task: weight out of range")
)

type Clock func() time.Time

func SystemClock() time.Time { return time.Now() }

func FixedClock(at time.Time) Clock { return func() time.Time { return at } }

type Task struct {
	Tenant     string
	Cost       float64
	Seq        int64
	SubmittedAt time.Time
}

func New(tenant string, cost float64, seq int64, now Clock) (Task, error) {
	if tenant == "" {
		return Task{}, ErrEmptyTenant
	}
	if cost < 0 || cost > MaxCost || cost != cost {
		return Task{}, ErrBadCost
	}
	at := time.Time{}
	if now != nil {
		at = now()
	}
	return Task{Tenant: tenant, Cost: cost, Seq: seq, SubmittedAt: at}, nil
}

func ValidateWeight(weight float64) error {
	if weight <= 0 || weight > MaxWeight || weight != weight {
		return ErrBadWeight
	}
	return nil
}
