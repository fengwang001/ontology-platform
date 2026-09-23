package solve

import (
	"fmt"
	"sort"
	"strings"
)

// record renders the conflict on pkg and keeps the lexicographically
// smallest rendering, so the reported explanation depends only on the
// input, never on search order.
func (sch *search) record(pkg string) {
	text := explain(sch, pkg)
	if sch.best == "" || text < sch.best {
		sch.best = text
	}
}

// explain builds the constraint chain for the conflict on pkg:
// from a root requirement, through the package@version constraints that
// led here, down to the final incompatible pair (or the reason no
// version can satisfy the combined constraint).
func explain(sch *search, pkg string) string {
	sorted := append([]imposed(nil), sch.imp[pkg]...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].key() < sorted[j].key() })

	var b strings.Builder
	fmt.Fprintf(&b, "conflict on package %q:\n", pkg)

	// Find the first prefix whose intersection becomes empty.
	prefixAt := -1
	acc := sorted[0].con
	for i := 1; i < len(sorted); i++ {
		acc = acc.Intersect(sorted[i].con)
		if acc.IsEmpty() {
			prefixAt = i
			break
		}
	}
	if prefixAt >= 0 {
		// Helly for line intervals: some earlier constraint is pairwise
		// disjoint from sorted[prefixAt]; take the first such pair.
		culprit := sorted[prefixAt]
		other := sorted[0]
		for j := 0; j < prefixAt; j++ {
			if sorted[j].con.Intersect(culprit.con).IsEmpty() {
				other = sorted[j]
				break
			}
		}
		for _, line := range sch.chain(pkg, culprit) {
			b.WriteString("  " + line + "\n")
		}
		for _, line := range sch.chain(pkg, other) {
			b.WriteString("  " + line + "\n")
		}
		fmt.Fprintf(&b, "  %s: %q (from %s) is incompatible with %q (from %s)",
			pkg, culprit.raw, culprit.source(), other.raw, other.source())
		return b.String()
	}

	combined := acc
	for _, line := range sch.chain(pkg, sorted[len(sorted)-1]) {
		b.WriteString("  " + line + "\n")
	}
	if v, ok := sch.assign[pkg]; ok && !combined.Contains(v) {
		fmt.Fprintf(&b, "  %s: already selected %s@%s violates combined constraint %q",
			pkg, pkg, v, combined)
		return b.String()
	}
	var avail []string
	for _, v := range sch.g.Versions(pkg) {
		avail = append(avail, v.String())
	}
	fmt.Fprintf(&b, "  %s: no registered version satisfies combined constraint %q (available: %s)",
		pkg, combined, strings.Join(avail, ", "))
	return b.String()
}

// chain renders the provenance of one imposed constraint back to a root
// requirement, root first. Cycles are cut by a visited set.
func (sch *search) chain(target string, im imposed) []string {
	var lines []string
	cur, curTarget := im, target
	visited := map[string]bool{}
	for {
		if cur.pkg == "" {
			lines = append(lines, fmt.Sprintf("root requires %s %s", curTarget, cur.raw))
			break
		}
		lines = append(lines, fmt.Sprintf("%s@%s requires %s %s",
			cur.pkg, cur.ver, curTarget, cur.raw))
		if visited[cur.pkg] {
			break
		}
		visited[cur.pkg] = true
		ups := sch.imp[cur.pkg]
		if len(ups) == 0 {
			break
		}
		next := ups[0]
		for _, u := range ups[1:] {
			if u.key() < next.key() {
				next = u
			}
		}
		curTarget = cur.pkg
		cur = next
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}
