package ontology

import (
	"fmt"
	"sort"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
