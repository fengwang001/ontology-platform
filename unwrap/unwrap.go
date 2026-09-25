// Package unwrap expands a wrap-around modulo-M serial stream into a
// monotonic int64 absolute sequence; it depends only on sar.
package unwrap

import (
	"errors"
	"ontology/sar"
	"sync"
)

var ErrSelfCheck = errors.New("unwrap: self-check failed")

// Unwrapper is a goroutine-safe monotonic expander.
type Unwrapper struct {
	mu                      sync.RWMutex
	a                       *sar.Arith
	last                    int64  // absolute serial number, non-decreasing
	have                    bool   // at least one accepted value
	checksLast, checksTotal uint64 // unexported O(1) history-entry probes
}

func New(N int) (*Unwrapper, error) {
	a, err := sar.New(N)
	if err != nil {
		return nil, err
	}
	return &Unwrapper{a: a}, nil
}

func (u *Unwrapper) Width() int { u.mu.RLock(); defer u.mu.RUnlock(); return u.a.Width() }

func (u *Unwrapper) Last() (int64, bool) { u.mu.RLock(); defer u.mu.RUnlock(); return u.last, u.have }

func (u *Unwrapper) Cmp(x, y uint64) sar.Rel { return u.a.Cmp(x, y) }

// Feed expands one residue; rejects leave last untouched, one probe: O(1).
func (u *Unwrapper) Feed(s uint64) (int64, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checksLast, u.checksTotal = 1, u.checksTotal+1
	if err := u.a.CheckValid(s); err != nil {
		return 0, err
	}
	if !u.have {
		u.last, u.have = int64(s), true
		return u.last, nil
	}
	d := (s - (uint64(u.last) & u.a.Mask())) & u.a.Mask()
	switch r := u.a.Classify(d); r {
	case sar.Less:
		u.last += int64(d)
		fallthrough
	case sar.Equal:
		return u.last, nil
	case sar.Incomparable:
		return 0, sar.ErrIncomparable
	default:
		return 0, sar.ErrGreater
	}
}

// SelfCheck verifies the four invariants and the O(1) probe budget on
// built-in streams replayed on fresh expanders; the receiver is untouched.
func (u *Unwrapper) SelfCheck() (err error) {
	defer func() { // only ErrSelfCheck assertions become the returned error
		if r := recover(); r != nil {
			if e, ok := r.(error); ok && errors.Is(e, ErrSelfCheck) {
				err = ErrSelfCheck
			} else {
				panic(r)
			}
		}
	}()
	a := u.a
	must := func(ok bool) {
		if !ok {
			panic(ErrSelfCheck)
		}
	}
	anti := func(x, y uint64) bool { // invariant 2: antisymmetry
		r1, r2 := a.Cmp(x, y), a.Cmp(y, x)
		return (x == y && r1 == sar.Equal) || (r1 == sar.Less && r2 == sar.Greater) ||
			(r1 == sar.Greater && r2 == sar.Less) || (r1 == sar.Incomparable && r2 == sar.Incomparable)
	}
	for _, x := range []uint64{0, a.Half(), a.Mask()} { // boundary pairs cover all regions
		for _, d := range []uint64{1, a.Half() - 1, a.Half(), a.Half() + 1, a.Mask()} {
			must(anti(x, (x+d)&a.Mask()))
		}
	}
	fl, _ := New(a.Width())
	if a.Width() >= 2 { // long strictly increasing stream, wraps many times; N=1 only rejects
		count := uint64(5000)
		if a.Mod() < 1700 {
			count = 3*a.Mod() + 5
		}
		for i := uint64(0); i < count; i++ {
			v, e := fl.Feed(i & a.Mask())
			must(e == nil && v == int64(i)) // invariants 1,3: absolute value is i
		}
		must(fl.checksLast == 1 && fl.checksTotal == count)
	}
	replay := func(st []uint64) *Unwrapper { // invariants 1,4 vs the hand rule
		f, _ := New(a.Width())
		var ref int64
		have := false
		for _, s := range st {
			got, gerr := f.Feed(s)
			l, lh := f.Last()
			if !have {
				must(gerr == nil && got == int64(s) && l == got && lh)
				ref, have = int64(s), true
				continue
			}
			d := (s - (uint64(ref) & a.Mask())) & a.Mask()
			r := a.Classify(d)
			if r == sar.Less {
				ref += int64(d)
			}
			switch r {
			case sar.Equal, sar.Less:
				must(gerr == nil && got == ref)
			case sar.Incomparable:
				must(errors.Is(gerr, sar.ErrIncomparable))
			default:
				must(errors.Is(gerr, sar.ErrGreater))
			}
			must(l == ref && lh == have)
		}
		return f
	}
	mixed := []uint64{0, a.Half()} // first value; then half-circle Incomparable
	if a.Width() >= 2 {            // then Less, Greater(rewind), Equal repeat, Less
		mixed = append(mixed, a.Half()-1, 0, a.Half()-1, a.Half())
	}
	fm := replay(mixed)
	must(fm.checksLast == 1 && fm.checksTotal == uint64(len(mixed)))
	if a.Width() >= 4 {
		replay([]uint64{13, 14, 15, 0, 1, 2, 10, 3}) // section 3 trace
	}
	f, _ := New(a.Width()) // out of range before first value leaves no trace
	_, e := f.Feed(a.Mod())
	must(errors.Is(e, sar.ErrOutOfRange))
	l, h := f.Last()
	must(!h && l == 0)
	for _, n := range []int{0, -1, 64, 100} {
		_, e := New(n)
		must(errors.Is(e, sar.ErrWidth))
	}
	return nil
}
