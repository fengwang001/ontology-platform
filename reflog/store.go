package reflog

// store holds every commit and content object currently present.
type store struct {
	commits map[CommitID]*Commit
	objects map[ObjectID]*Object
}

func newStore() *store {
	return &store{
		commits: make(map[CommitID]*Commit),
		objects: make(map[ObjectID]*Object),
	}
}

// putCommit inserts c; the first write wins, so an existing entry is
// kept (and its FirstWrittenAt preserved). It reports whether the
// commit was newly inserted.
func (s *store) putCommit(c *Commit) bool {
	if _, ok := s.commits[c.ID]; ok {
		return false
	}
	s.commits[c.ID] = c
	return true
}

// putObject inserts o; the first write wins.
func (s *store) putObject(o *Object) bool {
	if _, ok := s.objects[o.ID]; ok {
		return false
	}
	s.objects[o.ID] = o
	return true
}

func (s *store) hasCommit(id CommitID) bool {
	_, ok := s.commits[id]
	return ok
}

// ancestorSet returns every commit reachable from root by following
// parent edges, including root itself. Dangling parent references
// (commits not present in the store) are skipped.
func (s *store) ancestorSet(root CommitID) map[CommitID]bool {
	seen := make(map[CommitID]bool)
	stack := []CommitID{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		c, ok := s.commits[id]
		if !ok {
			continue
		}
		seen[id] = true
		stack = append(stack, c.Parents...)
	}
	return seen
}
