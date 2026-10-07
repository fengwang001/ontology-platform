package bitemporal

import "sort"

// sortedFields converts a field map to key-sorted canonical pairs.
func sortedFields(m map[string]any) [][2]any {
	out := make([][2]any, 0, len(m))
	for k, val := range m {
		out = append(out, [2]any{k, canonicalScalar(val)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].(string) < out[j][0].(string) })
	return out
}

func canonicalScalar(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	default:
		return v
	}
}
