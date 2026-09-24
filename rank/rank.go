// Package rank scores one server candidate against one accept item.
package rank

import "ontology/mtype"

// Score reports whether item matches candidate and the match specificity:
// 2 exact, 1 type wildcard, 0 full wildcard. Parameter sets (q excluded,
// already extracted by mtype) must be equal on both sides.
func Score(candidate, item mtype.Type) (int, bool) {
	if item.Type != "*" && item.Type != candidate.Type {
		return 0, false
	}
	if item.Subtype != "*" && item.Subtype != candidate.Subtype {
		return 0, false
	}
	if !paramsEqual(candidate.Params, item.Params) {
		return 0, false
	}
	spec := 0
	if item.Type != "*" {
		spec++
		if item.Subtype != "*" {
			spec++
		}
	}
	return spec, true
}

func paramsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
