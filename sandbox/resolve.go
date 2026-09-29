package sandbox

// maxSymlinkFollows bounds the number of symbolic links followed during a
// single resolution. Exactly 40 follows are allowed; attempting the 41st
// fails with ErrTooManyLinks, which also catches link cycles.
const maxSymlinkFollows = 40

// Resolve resolves path with physical (realpath-like) semantics inside the
// sandbox root:

// Empty segments and "." are ignored. ".." pops back to the *physical*
// parent directory, so after traversing a symbolic link it undoes the link
// target rather than the link's location; ".." at the root stays at root.
// Absolute link targets (starting with "/") are rooted at the sandbox root;
// relative targets are rooted at the directory containing the link.
// Intermediate symbolic links are always followed; the final component is
// followed only when followFinal is true.
//
// The call observes a single immutable snapshot of the tree, so the result
// is always equal to a fully serial resolution at one point in time.
func (t *Tree) Resolve(path string, followFinal bool) (Resolution, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.resolveLocked(path, followFinal)
}

type pendingSegment struct {
	name string
}

func (t *Tree) resolveLocked(path string, followFinal bool) (Resolution, error) {
	if path == "" {
		return Resolution{}, ErrEmptyPath
	}

	stack := []*Node{t.root}
	worklist := []pendingSegment{}
	for _, seg := range splitSegments(path) {
		worklist = append(worklist, pendingSegment{name: seg})
	}

	follows := 0
	for len(worklist) > 0 {
		seg := worklist[0].name
		worklist = worklist[1:]
		last := len(worklist) == 0

		if seg == ".." {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		current := stack[len(stack)-1]
		child, ok := current.children[seg]
		if !ok {
			return Resolution{}, ErrNotExist
		}

		switch child.kind {
		case KindDirectory:
			stack = append(stack, child)
		case KindFile:
			if !last {
				return Resolution{}, ErrNotDirectory
			}
			return Resolution{Node: child, Path: t.physicalPath(child)}, nil
		case KindSymlink:
			if last && !followFinal {
				return Resolution{Node: child, Path: t.physicalPath(child)}, nil
			}
			if follows >= maxSymlinkFollows {
				return Resolution{}, ErrTooManyLinks
			}
			follows++

			var injected []pendingSegment
			if len(child.target) > 0 && child.target[0] == '/' {
				// Absolute targets never leave the sandbox: the sandbox
				// root itself is the only root that exists.
				stack = []*Node{t.root}
			}
			for _, part := range splitSegments(child.target) {
				injected = append(injected, pendingSegment{name: part})
			}
			worklist = append(injected, worklist...)
		}
	}

	top := stack[len(stack)-1]
	return Resolution{Node: top, Path: t.physicalPath(top)}, nil
}
