package equip

import (
	"errors"
	"sort"
)

var (
	ErrEmptyName    = errors.New("equipment type is empty")
	ErrExists       = errors.New("equipment type already exists")
	ErrBadCount     = errors.New("equipment count must be in [1,100]")
	ErrBadSterilize = errors.New("equipment sterilize time must be in [0,240]")
)

type Type struct {
	Name      string
	Count     int
	Sterilize int64
}

type Use struct {
	ID    string
	Start int64
	End   int64
	Count int
}

type Pool struct {
	types map[string]Type
}

func NewPool() *Pool {
	return &Pool{types: map[string]Type{}}
}

func (p *Pool) Add(name string, count int, sterilize int64) error {
	if name == "" {
		return ErrEmptyName
	}
	if count < 1 || count > 100 {
		return ErrBadCount
	}
	if sterilize < 0 || sterilize > 240 {
		return ErrBadSterilize
	}
	if _, ok := p.types[name]; ok {
		return ErrExists
	}
	p.types[name] = Type{Name: name, Count: count, Sterilize: sterilize}
	return nil
}

func (p *Pool) Get(name string) (Type, bool) {
	got, ok := p.types[name]
	return got, ok
}

func (p *Pool) Names() []string {
	names := make([]string, 0, len(p.types))
	for name := range p.types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func OccupiedEnd(end int64, sterilize int64) int64 {
	return end + sterilize
}

func Intersects(startA, endA, startB, endB int64) bool {
	return startA < endB && startB < endA
}

func Shortfall(candidate Use, uses []Use, capacity int, sterilize int64) int {
	candidateEnd := OccupiedEnd(candidate.End, sterilize)
	active := make([]Use, 0, len(uses))
	for _, use := range uses {
		useEnd := OccupiedEnd(use.End, sterilize)
		if Intersects(candidate.Start, candidateEnd, use.Start, useEnd) {
			active = append(active, Use{Start: use.Start, End: useEnd, Count: use.Count})
		}
	}

	worst := 0
	add := func(at int64) {
		used := candidate.Count
		for _, use := range active {
			if use.Start <= at && at < use.End {
				used += use.Count
			}
		}
		if used-capacity > worst {
			worst = used - capacity
		}
	}

	add(candidate.Start)
	for _, use := range active {
		if candidate.Start < use.Start && use.Start < candidateEnd {
			add(use.Start)
		}
	}
	return worst
}
