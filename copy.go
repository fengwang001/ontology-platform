package projection

// cloneValue returns a deep, independent copy of v. Only map[string]any,
// []any and scalar values are produced: callers can never mutate the input
// object through a projection result, or vice versa.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, child := range t {
			m[k] = cloneValue(child)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, child := range t {
			s[i] = cloneValue(child)
		}
		return s
	default:
		return v
	}
}

// visibleMap reports whether a projection result is an object that still
// holds at least one attribute.
func visibleMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return m, len(m) > 0
}
