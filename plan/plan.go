package plan

import "errors"

var (
	ErrInvalidArgument = errors.New("plan: invalid argument")
	ErrNoRange         = errors.New("plan: batch size out of any range")
)

type Severity int

const (
	Normal Severity = iota
	Tightened
	Reduced
	Suspended
)

type Scheme struct {
	N  int
	Ac int
	Re int
}

type Range struct {
	Lo        int
	Hi        int
	Normal    Scheme
	Tightened Scheme
	Reduced   Scheme
}

type Table struct {
	ranges []Range
	lr     int
}

func New(ranges []Range, lr int) (*Table, error) {
	if lr < 0 || len(ranges) == 0 {
		return nil, ErrInvalidArgument
	}
	for i := range ranges {
		r := ranges[i]
		if r.Lo < 1 || r.Hi > 1_000_000 || r.Lo > r.Hi {
			return nil, ErrInvalidArgument
		}
		if err := validateScheme(r.Normal, true); err != nil {
			return nil, err
		}
		if err := validateScheme(r.Tightened, true); err != nil {
			return nil, err
		}
		if err := validateScheme(r.Reduced, false); err != nil {
			return nil, err
		}
		for j := 0; j < i; j++ {
			o := ranges[j]
			if r.Lo <= o.Hi && o.Lo <= r.Hi {
				return nil, ErrInvalidArgument
			}
		}
	}
	cp := make([]Range, len(ranges))
	copy(cp, ranges)
	return &Table{ranges: cp, lr: lr}, nil
}

func validateScheme(sc Scheme, contiguous bool) error {
	if sc.N < 1 || sc.Ac < 0 || sc.Re <= sc.Ac {
		return ErrInvalidArgument
	}
	if contiguous && sc.Re != sc.Ac+1 {
		return ErrInvalidArgument
	}
	return nil
}

func (t *Table) SchemeFor(batchN int, s Severity) (Scheme, error) {
	if batchN < 1 || batchN > 1_000_000 {
		return Scheme{}, ErrInvalidArgument
	}
	for _, r := range t.ranges {
		if batchN < r.Lo || batchN > r.Hi {
			continue
		}
		var sc Scheme
		switch s {
		case Normal:
			sc = r.Normal
		case Tightened:
			sc = r.Tightened
		case Reduced:
			sc = r.Reduced
		default:
			return Scheme{}, ErrInvalidArgument
		}
		if sc.N > batchN {
			sc.N = batchN
		}
		return sc, nil
	}
	return Scheme{}, ErrNoRange
}

func (t *Table) LR() int { return t.lr }
