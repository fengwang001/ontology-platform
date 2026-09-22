package solve

import (
	"fmt"
	"strings"

	"ontology/rng"
)

// UnknownPackageError：约束引用了未登记的包（与语法错误互不相同）。
type UnknownPackageError struct{ Package string }

func (e *UnknownPackageError) Error() string {
	return fmt.Sprintf("constraint references unregistered package %q", e.Package)
}

func (e *UnknownPackageError) Unwrap() error { return graphErrUnknown }

// conflict 依据确定性的互斥条目对构造冲突链。
func (st *search) conflict(pkg string, pair [2]rng.Entry) error {
	var chain []ChainLink
	added := map[string]bool{}
	addChain := func(e rng.Entry) {
		for _, l := range st.chainFor(e) {
			key := l.From + "|" + l.Constraint + "|" + l.Target
			if !added[key] {
				added[key] = true
				chain = append(chain, l)
			}
		}
	}
	if pair[1].Origin.Target != "" {
		if pair[0].Origin.Target != "" {
			addChain(pair[0])
		}
		addChain(pair[1])
	} else {
		addChain(pair[1])
	}
	var reason string
	if pair[0].Origin.Target == "" {
		reason = fmt.Sprintf("no candidate of %s satisfies constraint %s", pkg, pair[1].Origin.Raw)
	} else {
		reason = fmt.Sprintf("constraints %s and %s on %s are incompatible",
			pair[0].Origin.Raw, pair[1].Origin.Raw, pkg)
	}
	return &ConflictError{Package: pkg, Reason: reason, Chain: chain}
}

// chainFor 沿条目来源回溯：root → ... → 该条目本身。
func (st *search) chainFor(e rng.Entry) []ChainLink {
	if e.Origin.Pkg == "" {
		return []ChainLink{{From: "<root>", Constraint: e.Origin.Raw, Target: e.Origin.Target}}
	}
	parent := st.entryProducing(e.Origin.Pkg)
	var prefix []ChainLink
	if parent.Origin.Target != "" {
		prefix = st.chainFor(parent)
	}
	return append(prefix, ChainLink{
		From:       e.Origin.Pkg + "@" + e.Origin.V.String(),
		Constraint: e.Origin.Raw,
		Target:     e.Origin.Target,
	})
}

// entryProducing 返回当前域内引入了 pkg 的、按 originLess 最小的条目。
func (st *search) entryProducing(pkg string) rng.Entry {
	var best rng.Entry
	found := false
	for _, e := range st.entries[pkg] {
		if !found || originLess(e.Origin, best.Origin) {
			best, found = e, true
		}
	}
	return best
}

func (e *ConflictError) Error() string {
	var b strings.Builder
	b.WriteString(e.Reason)
	if len(e.Chain) > 0 {
		b.WriteString("\n  chain:")
		for _, l := range e.Chain {
			fmt.Fprintf(&b, "\n    %s --(%s)--> %s", l.From, l.Constraint, l.Target)
		}
	}
	return b.String()
}
