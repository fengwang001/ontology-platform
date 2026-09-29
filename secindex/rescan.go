package secindex

import (
	"fmt"
	"sort"
	"strings"
)

// RescanMismatch describes the first divergence between the maintained index
// and an index rebuilt from scratch by scanning the whole record table.
type RescanMismatch struct {
	// Where identifies the check that failed, e.g. "groups" or "group:42".
	Where string
	Got   []string
	Want  []string
}

func (mm RescanMismatch) Error() string {
	return fmt.Sprintf("secindex: rescan mismatch at %s: maintained=%v rebuilt=%v",
		mm.Where, mm.Got, mm.Want)
}

// VerifyByRescan ignores the maintained index, rebuilds the expected one by
// scanning every record, and compares the two. It returns nil when the
// maintained index is exactly the rescan result and a *RescanMismatch
// describing the first difference otherwise. Intended for local verification
// after incremental maintenance (updates/deletes/batches).
func (m *Maintainer) VerifyByRescan() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rebuilt := make(map[int64][]string, len(m.index))
	for key, value := range m.records {
		rebuilt[value] = append(rebuilt[value], key)
	}
	for _, keys := range rebuilt {
		sort.Strings(keys)
	}

	maintained := make(map[int64][]string, len(m.index))
	for value, keys := range m.index {
		maintained[value] = append([]string(nil), keys...)
	}

	values := make(map[int64]struct{}, len(rebuilt)+len(maintained))
	for v := range rebuilt {
		values[v] = struct{}{}
	}
	for v := range maintained {
		values[v] = struct{}{}
	}
	ordered := make([]int64, 0, len(values))
	for v := range values {
		ordered = append(ordered, v)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	for _, v := range ordered {
		if !equalStrings(maintained[v], rebuilt[v]) {
			return RescanMismatch{
				Where: fmt.Sprintf("group:%d", v),
				Got:   orEmpty(maintained[v]),
				Want:  orEmpty(rebuilt[v]),
			}
		}
	}

	var totalMaintained int
	for _, keys := range maintained {
		totalMaintained += len(keys)
	}
	if totalMaintained != len(m.records) {
		return RescanMismatch{
			Where: "cardinality",
			Got:   []string{fmt.Sprintf("%d indexed entries", totalMaintained)},
			Want:  []string{fmt.Sprintf("%d records", len(m.records))},
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func orEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// formatGroups renders index groups for test and debug logs, e.g.
// "10:[b d] 20:[a]".
func formatGroups(groups []IndexGroup) string {
	if len(groups) == 0 {
		return "<empty>"
	}
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%d:[%s]", g.Value, strings.Join(g.Keys, " ")))
	}
	return strings.Join(parts, " ")
}
