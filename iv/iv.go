package iv

import (
	"errors"
	"math"
)

var (
	ErrInvalid  = errors.New("invalid interval")
	ErrOverflow = errors.New("endpoint overflow")
)

type Interval struct {
	lo, hi int64
}

func New(lo, hi int64) (Interval, error) {
	if lo > hi {
		return Interval{}, ErrInvalid
	}
	return Interval{lo: lo, hi: hi}, nil
}

func NewPoint(x int64) (Interval, error) {
	if x == math.MaxInt64 {
		return Interval{}, ErrOverflow
	}
	return Interval{lo: x, hi: x + 1}, nil
}

func (i Interval) Lo() int64             { return i.lo }
func (i Interval) Hi() int64             { return i.hi }
func (i Interval) Empty() bool           { return i.lo == i.hi }
func (i Interval) Contains(x int64) bool { return i.lo <= x && x < i.hi }

func (i Interval) Touches(o Interval) bool {
	if i.Empty() || o.Empty() {
		return false
	}
	return i.hi >= o.lo && o.hi >= i.lo
}

func (i Interval) Adjacent(o Interval) bool {
	if i.Empty() || o.Empty() {
		return false
	}
	return i.hi == o.lo || o.hi == i.lo
}

func (i Interval) Overlaps(o Interval) bool {
	if i.Empty() || o.Empty() {
		return false
	}
	return i.hi > o.lo && o.hi > i.lo
}

func (i Interval) ContainsSpan(o Interval) bool {
	if o.Empty() {
		return true
	}
	if i.Empty() {
		return false
	}
	return i.lo <= o.lo && o.hi <= i.hi
}
