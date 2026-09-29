// Package zone stores row-group statistics and performs predicate pruning.
package zone

import "cmp"

type Ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64 | ~string
}

type Op uint8

const (
	Eq Op = iota + 1
	Lt
	Le
	Gt
	Ge
	In
	IsNull
	IsNotNull
)

type Zone[T Ordered] struct {
	Has       bool
	Min       T
	Max       T
	NullCount int
	Rows      int
}

type Predicate[T Ordered] interface {
	Match(value T, null bool) bool
	CanMatch(zone Zone[T]) bool
}

type Comparison[T Ordered] struct {
	Op    Op
	Value T
}

type InList[T Ordered] struct {
	Values []T
}

type NullCheck[T Ordered] struct {
	IsNull bool
}

type And[T Ordered] struct {
	Items []Predicate[T]
}

func Build[T Ordered](values []T, present []bool) Zone[T] {
	z := Zone[T]{Rows: len(values), NullCount: countMissing(present, len(values))}
	for i, value := range values {
		if i < len(present) && !present[i] {
			continue
		}
		if !z.Has {
			z.Min, z.Max, z.Has = value, value, true
			continue
		}
		z.Min = min(z.Min, value)
		z.Max = max(z.Max, value)
	}
	return z
}

func countMissing(present []bool, rows int) int {
	if len(present) < rows {
		return rows - len(present)
	}
	missing := 0
	for _, ok := range present[:rows] {
		if !ok {
			missing++
		}
	}
	return missing
}

func (p Comparison[T]) Match(value T, null bool) bool {
	if null {
		return false
	}
	switch p.Op {
	case Eq:
		return value == p.Value
	case Lt:
		return cmp.Less(value, p.Value)
	case Le:
		return value == p.Value || cmp.Less(value, p.Value)
	case Gt:
		return cmp.Less(p.Value, value)
	case Ge:
		return value == p.Value || cmp.Less(p.Value, value)
	default:
		return false
	}
}

func (p Comparison[T]) CanMatch(z Zone[T]) bool {
	if !z.Has {
		return false
	}
	switch p.Op {
	case Eq:
		return !cmp.Less(p.Value, z.Min) && !cmp.Less(z.Max, p.Value)
	case Lt:
		return cmp.Less(z.Min, p.Value)
	case Le:
		return cmp.Less(z.Min, p.Value) || z.Min == p.Value
	case Gt:
		return cmp.Less(p.Value, z.Max)
	case Ge:
		return cmp.Less(p.Value, z.Max) || z.Max == p.Value
	default:
		return false
	}
}

func (p InList[T]) Match(value T, null bool) bool {
	if null {
		return false
	}
	for _, candidate := range p.Values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (p InList[T]) CanMatch(z Zone[T]) bool {
	if !z.Has {
		return false
	}
	for _, value := range p.Values {
		if !cmp.Less(value, z.Min) && !cmp.Less(z.Max, value) {
			return true
		}
	}
	return false
}

func (p NullCheck[T]) Match(_ T, null bool) bool {
	return null == p.IsNull
}

func (p NullCheck[T]) CanMatch(z Zone[T]) bool {
	if p.IsNull {
		return z.NullCount > 0
	}
	return z.Rows-z.NullCount > 0
}

func (p And[T]) Match(value T, null bool) bool {
	for _, item := range p.Items {
		if !item.Match(value, null) {
			return false
		}
	}
	return true
}

func (p And[T]) CanMatch(z Zone[T]) bool {
	for _, item := range p.Items {
		if !item.CanMatch(z) {
			return false
		}
	}
	return true
}
