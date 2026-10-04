package basket

import (
	"errors"
	"slices"
)

var ErrInvalidArgument = errors.New("invalid argument")

type Flag string

const (
	Forbidden Flag = "N"
	Allowed   Flag = "A"
	Mandatory Flag = "M"
)

type Item struct {
	Sym   string
	Qty   int64
	Flag  Flag
	Prem  int64
	Fixed int64
}

type Basket struct {
	items    []Item
	cashDiff int64
	maxRatio int64
	maxUnits int64
}

func New(items []Item, cashDiff int64, maxRatio int64, maxDailyUnits int64) (*Basket, error) {
	if len(items) < 1 || len(items) > 500 {
		return nil, ErrInvalidArgument
	}
	if cashDiff < -1_000_000_000 || cashDiff > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if maxRatio < 0 || maxRatio > 100 || maxDailyUnits < 1 || maxDailyUnits > 1_000_000 {
		return nil, ErrInvalidArgument
	}

	seen := make(map[string]struct{}, len(items))
	sorted := append([]Item(nil), items...)
	for _, item := range sorted {
		if item.Sym == "" || item.Qty < 1 || item.Qty > 1_000_000 {
			return nil, ErrInvalidArgument
		}
		if _, ok := seen[item.Sym]; ok {
			return nil, ErrInvalidArgument
		}
		seen[item.Sym] = struct{}{}
		switch item.Flag {
		case Forbidden:
			if item.Prem != 0 || item.Fixed != 0 {
				return nil, ErrInvalidArgument
			}
		case Allowed:
			if item.Prem < 0 || item.Prem > 5000 || item.Fixed != 0 {
				return nil, ErrInvalidArgument
			}
		case Mandatory:
			if item.Prem != 0 || item.Fixed < 1 || item.Fixed > 1_000_000_000_000 {
				return nil, ErrInvalidArgument
			}
		default:
			return nil, ErrInvalidArgument
		}
	}
	slices.SortFunc(sorted, func(left, right Item) int {
		if left.Sym < right.Sym {
			return -1
		}
		return 1
	})

	return &Basket{
		items:    sorted,
		cashDiff: cashDiff,
		maxRatio: maxRatio,
		maxUnits: maxDailyUnits,
	}, nil
}

func (b *Basket) Items() []Item {
	return append([]Item(nil), b.items...)
}

func (b *Basket) CashDiff() int64 { return b.cashDiff }

func (b *Basket) MaxRatio() int64 { return b.maxRatio }

func (b *Basket) MaxDailyUnits() int64 { return b.maxUnits }
