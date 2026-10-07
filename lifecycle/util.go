package lifecycle

import "sort"

func sortStrings(s []string) {
	sort.Strings(s)
}

func equalValue(a, b AttrValue) bool {
	return a == b
}

func containsState(set []string, st string) bool {
	for _, s := range set {
		if s == st {
			return true
		}
	}
	return false
}
