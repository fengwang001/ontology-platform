package iv

import "errors"

var (
	ErrInvalid  = errors.New("invalid interval")
	ErrOverflow = errors.New("endpoint overflow")
)

type Interval struct {
	Lo int64
	Hi int64
}

func New(lo, hi int64) (Interval, error) {
	in := Interval{Lo: lo, Hi: hi}
	if lo > hi {
		return Interval{}, ErrInvalid
	}
	return in, nil
}

func (in Interval) Valid() bool {
	return in.Lo <= in.Hi
}

func (in Interval) Empty() bool {
	return in.Lo == in.Hi
}

func (in Interval) Overlaps(other Interval) bool {
	return in.Valid() && other.Valid() && !in.Empty() && !other.Empty() &&
		in.Lo < other.Hi && other.Lo < in.Hi
}

func (in Interval) Adjacent(other Interval) bool {
	return in.Valid() && other.Valid() && !in.Empty() && !other.Empty() &&
		(in.Hi == other.Lo || other.Hi == in.Lo)
}

func (in Interval) Contains(other Interval) bool {
	return in.Valid() && other.Valid() &&
		in.Lo <= other.Lo && other.Hi <= in.Hi
}

func (in Interval) Disjoint(other Interval) bool {
	return in.Valid() && other.Valid() &&
		!in.Overlaps(other) && !in.Adjacent(other)
}
