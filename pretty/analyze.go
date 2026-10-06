package pretty

import (
	"fmt"
	"strings"
)

const (
	maxLineWidth  = 10000
	maxDepth      = 1000
	maxTotalWidth = 10_000_000
	// rawSafetyCap bounds the untrimmed emitted output so that pathological
	// inputs (e.g. exponentially shared fragments) terminate instead of
	// exhausting memory. See DESIGN.md.
	rawSafetyCap = 100_000_000
	// widthCap caps accumulated width annotations; anything above the
	// maximum line width is equivalent to "does not fit".
	widthCap = maxLineWidth + 1
)

// nodeInfo holds per-node annotations computed once per render. They let the
// fits scan collapse whole subtrees (notably undecided groups) in O(1).
type nodeInfo struct {
	flatW   int  // width when rendered flat, capped at widthCap
	altW    int  // width of the broken variant (Cond nodes only)
	headW   int  // flat width before the first forced newline, capped
	depth   int  // nesting depth of the expanded subtree, capped at maxDepth+1
	hasHard bool // subtree contains a forced newline
	hasStop bool // subtree contains anything the fits scan must inspect
}

type analysis struct {
	info         map[Doc]nodeInfo
	invalid      error  // first invalid-parameter problem found
	unregistered string // first unregistered fragment name found
}

func capWidth(w int) int {
	if w > widthCap {
		return widthCap
	}
	return w
}

func capDepth(d int) int {
	if d > maxDepth+1 {
		return maxDepth + 1
	}
	return d
}

func (a *analysis) setInvalid(format string, args ...any) {
	if a.invalid == nil {
		a.invalid = fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
	}
}

// analyze walks the document (expanding registered fragments, which form a
// DAG) iteratively in post-order, validating parameters and computing
// annotations. It never recurses, so arbitrarily deep inputs are safe.
func analyze(root Doc, frags map[string]Doc) *analysis {
	a := &analysis{info: make(map[Doc]nodeInfo)}
	if root == nil {
		a.setInvalid("nil document")
		return a
	}
	seen := make(map[Doc]bool)
	stack := make([]Doc, 0, 64)
	push := func(d Doc) {
		if d == nil {
			a.setInvalid("nil child node")
			return
		}
		if seen[d] {
			return
		}
		seen[d] = true
		stack = append(stack, d)
	}
	push(root)
	expanded := make(map[Doc]bool)
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		if expanded[top] {
			stack = stack[:len(stack)-1]
			a.combine(top, frags)
			continue
		}
		expanded[top] = true
		a.checkNode(top, frags)
		for _, c := range childrenOf(top, frags) {
			push(c)
		}
	}
	return a
}

// checkNode records node-local parameter violations and unregistered refs.
func (a *analysis) checkNode(d Doc, frags map[string]Doc) {
	switch n := d.(type) {
	case *textNode:
		if strings.ContainsRune(n.s, '\n') {
			a.setInvalid("text contains a newline character: %q", n.s)
		}
	case *condNode:
		if strings.ContainsRune(n.broken, '\n') || strings.ContainsRune(n.flat, '\n') {
			a.setInvalid("conditional text contains a newline character")
		}
	case *indentNode:
		if n.n < 0 {
			a.setInvalid("negative indent increment %d", n.n)
		}
	case *refNode:
		if n.name == "" {
			a.setInvalid("empty fragment name in reference")
		} else if _, ok := frags[n.name]; !ok {
			if a.unregistered == "" {
				a.unregistered = n.name
			}
		}
	}
}

func childrenOf(d Doc, frags map[string]Doc) []Doc {
	switch n := d.(type) {
	case *indentNode:
		return []Doc{n.body}
	case *alignNode:
		return []Doc{n.body}
	case *groupNode:
		return []Doc{n.body}
	case *seqNode:
		return n.parts
	case *refNode:
		if target, ok := frags[n.name]; ok {
			return []Doc{target}
		}
	}
	return nil
}

// combine computes the annotation of d from its (already computed) children.
func (a *analysis) combine(d Doc, frags map[string]Doc) {
	get := func(c Doc) nodeInfo {
		if c == nil {
			return nodeInfo{}
		}
		return a.info[c]
	}
	var inf nodeInfo
	switch n := d.(type) {
	case *textNode:
		w := capWidth(Width(n.s))
		inf = nodeInfo{flatW: w, headW: w, depth: 1}
	case *spaceNode:
		inf = nodeInfo{flatW: 1, headW: 1, depth: 1, hasStop: true}
	case *softNode:
		inf = nodeInfo{depth: 1, hasStop: true}
	case *hardNode:
		inf = nodeInfo{depth: 1, hasHard: true, hasStop: true}
	case *condNode:
		fw := capWidth(Width(n.flat))
		inf = nodeInfo{flatW: fw, altW: capWidth(Width(n.broken)), headW: fw, depth: 1, hasStop: true}
	case *indentNode:
		c := get(n.body)
		inf = c
		inf.depth = capDepth(c.depth + 1)
	case *alignNode:
		c := get(n.body)
		inf = c
		inf.depth = capDepth(c.depth + 1)
	case *groupNode:
		c := get(n.body)
		inf = c
		inf.depth = capDepth(c.depth + 1)
		inf.hasStop = true
	case *seqNode:
		inf.depth = 1
		hardSeen := false
		for _, p := range n.parts {
			c := get(p)
			inf.flatW = capWidth(inf.flatW + c.flatW)
			if !hardSeen {
				inf.headW = capWidth(inf.headW + c.headW)
			}
			hardSeen = hardSeen || c.hasHard
			inf.hasHard = inf.hasHard || c.hasHard
			inf.hasStop = inf.hasStop || c.hasStop
			inf.depth = capDepth(max(inf.depth, c.depth+1))
		}
	case *refNode:
		if target, ok := frags[n.name]; ok {
			// Expansion replaces the reference with the fragment root, so
			// the reference adds no extra depth level.
			inf = a.info[target]
		} else {
			inf = nodeInfo{depth: 1}
		}
	}
	a.info[d] = inf
}

func (a *analysis) depthOf(root Doc) int {
	if root == nil {
		return 0
	}
	return a.info[root].depth
}
