package coercion

import "reflect"

func (c *context) convertSlice(value any) (any, error) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		c.invalid(value, c.target.Kind, "expected a slice")
		return emptySlice(c.target.Kind), c.err()
	}

	length := rv.Len()
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		if c.target.Nullable {
			c.sliceNil = true
			return nil, nil
		}
		c.report(&Error{Category: Null, Target: c.target.Kind, Original: value,
			Detail: "non-nullable target received nil slice; empty slices remain present"})
		return nil, c.err()
	}

	elementKind := c.target.Kind.element()
	values := make([]any, 0, length)
	for i := 0; i < length; i++ {
		element := rv.Index(i).Interface()
		if element == nil {
			err := &Error{Category: Null, Target: elementKind, Index: i, hasIndex: true,
				Original: nil, Detail: "slice element is explicitly null; element skipped"}
			if c.mode == Strict {
				c.errors = append(c.errors, err)
			} else {
				c.degradations = append(c.degradations, Degradation{
					Category: err.Category, Index: i, hasIndex: true, Original: nil,
					Detail: err.Detail, Skipped: true,
				})
			}
			continue
		}
		converted, ok := c.convertIndexed(element, elementKind, i)
		if ok {
			values = append(values, converted)
		}
	}

	return c.buildSlice(elementKind, values), c.err()
}

func (c *context) convertIndexed(value any, kind Kind, index int) (any, bool) {
	element := &context{mode: c.mode, target: Target{Kind: kind}}
	converted := element.convertScalar(value, kind)

	if c.mode == Strict {
		for _, err := range element.errors {
			err.Index = index
			err.hasIndex = true
			err.Target = kind
			c.errors = append(c.errors, err)
		}
		return converted, len(element.errors) == 0
	}

	for _, record := range element.degradations {
		record.Index = index
		record.hasIndex = true
		c.degradations = append(c.degradations, record)
	}
	for _, err := range element.errors {
		c.degradations = append(c.degradations, Degradation{
			Category: err.Category,
			Index:    index,
			hasIndex: true,
			Original: err.Original,
			Detail:   "index skipped: " + err.Detail,
			Skipped:  true,
		})
	}
	return converted, len(element.errors) == 0
}

func (c *context) buildSlice(kind Kind, values []any) any {
	switch kind {
	case String:
		out := make([]string, len(values))
		for i, value := range values {
			out[i] = value.(string)
		}
		return out
	case Int64Kind:
		out := make([]int64, len(values))
		for i, value := range values {
			out[i] = value.(int64)
		}
		return out
	case Float64Kind:
		out := make([]float64, len(values))
		for i, value := range values {
			out[i] = value.(float64)
		}
		return out
	case BoolKind:
		out := make([]bool, len(values))
		for i, value := range values {
			out[i] = value.(bool)
		}
		return out
	default:
		return emptySlice(c.target.Kind)
	}
}

func emptySlice(kind Kind) any {
	switch kind {
	case StringSlice:
		return []string{}
	case Int64Slice:
		return []int64{}
	case Float64Slice:
		return []float64{}
	case BoolSlice:
		return []bool{}
	default:
		return nil
	}
}
