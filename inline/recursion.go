package inline

// chain is the current root-to-site expansion path: the function names on this
// path only. It never sees functions outside the path, so direct-recursion and
// "back to self" checks cost O(path length), independent of the total number
// of functions in the program.
type chain struct {
	path []string
}

func newChain(root string) *chain {
	return &chain{path: []string{root}}
}

// directRecursion reports whether the site directly calls its own lexical
// owner. For a copied site the owner is the function whose body supplied the
// site (the last frame), so an inlined self-call is caught here as well.
func (c *chain) directRecursion(callee string) bool {
	return c.path[len(c.path)-1] == callee
}

// occurrencesAfterInlining is how many times callee would appear on the path
// if accepted (its existing occurrences plus the new frame).
func (c *chain) occurrencesAfterInlining(callee string) int {
	n := 1
	for _, f := range c.path {
		if f == callee {
			n++
		}
	}
	return n
}

// push/pop bracket one accepted inlining; sibling branches start from the same
// path and therefore count independently.
func (c *chain) push(callee string) { c.path = append(c.path, callee) }

func (c *chain) pop() { c.path = c.path[:len(c.path)-1] }

func (c *chain) depth() int { return len(c.path) - 1 }

// copy returns a value snapshot for reporting the deepest reached path.
func (c *chain) snapshot() []string {
	return append([]string(nil), c.path...)
}

// withAppended returns a new chain ending in callee; the receiver is unchanged,
// so sibling branches created from the same parent count independently.
func (c *chain) withAppended(callee string) *chain {
	p := make([]string, 0, len(c.path)+1)
	p = append(p, c.path...)
	p = append(p, callee)
	return &chain{path: p}
}
