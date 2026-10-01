package liveness

import "sort"

func newSet() map[string]struct{} { return make(map[string]struct{}) }

func setAdd(s map[string]struct{}, v string) { s[v] = struct{}{} }

func setContains(s map[string]struct{}, v string) bool {
	_, ok := s[v]
	return ok
}

func setCopy(s map[string]struct{}) map[string]struct{} {
	dst := make(map[string]struct{}, len(s))
	for v := range s {
		dst[v] = struct{}{}
	}
	return dst
}

func setUnion(dst, src map[string]struct{}) {
	for v := range src {
		dst[v] = struct{}{}
	}
}

func setSubtract(s, removed map[string]struct{}) {
	for v := range removed {
		delete(s, v)
	}
}

func setEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if _, ok := b[v]; !ok {
			return false
		}
	}
	return true
}

func sortedSlice(s map[string]struct{}) []string {
	if len(s) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func copyInts(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}

func copyInstructions(in []Instruction) []Instruction {
	if len(in) == 0 {
		return nil
	}
	out := make([]Instruction, len(in))
	for i, ins := range in {
		out[i] = Instruction{
			Uses: append([]string(nil), ins.Uses...),
			Defs: append([]string(nil), ins.Defs...),
		}
	}
	return out
}
