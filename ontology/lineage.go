package ontology

type lineOrigin struct {
	commitID string
	path     string
	line     int
}

type lineState struct {
	introduced lineOrigin
	parent     *lineState
}

type lineageResolver struct {
	cache map[stateKey][]*lineState
	stats resolverStats
}

type resolverStats struct {
	stateBuilds  int
	stateHits    int
	parentChecks int
}

type stateKey struct {
	commitID string
	path     string
}

func newLineageResolver() *lineageResolver {
	return &lineageResolver{cache: make(map[stateKey][]*lineState)}
}

func (x *lineageResolver) states(cm *commit, path string) []*lineState {
	key := stateKey{commitID: cm.id, path: path}
	if cached, ok := x.cache[key]; ok {
		x.stats.stateHits++
		return cached
	}
	x.stats.stateBuilds++

	childLines := splitLines(cm.files[path])
	result := make([]*lineState, len(childLines))
	matched := make([]bool, len(childLines))

	for _, parent := range cm.parents {
		x.stats.parentChecks++
		parentPath, ok := parentPathFor(cm, parent, path)
		if !ok {
			continue
		}
		parentStates := x.states(parent, parentPath)
		parentLines := splitLines(parent.files[parentPath])
		matches := matchLines(childLines, parentLines)
		for childIndex, parentIndex := range matches {
			if matched[childIndex] || parentIndex < 0 || parentIndex >= len(parentStates) {
				continue
			}
			result[childIndex] = &lineState{parent: parentStates[parentIndex]}
			matched[childIndex] = true
		}
	}

	for i := range result {
		if result[i] == nil {
			result[i] = &lineState{
				introduced: lineOrigin{
					commitID: cm.id,
					path:     path,
					line:     i + 1,
				},
			}
		}
	}

	x.cache[key] = result
	return result
}

func parentPathFor(cm, parent *commit, path string) (string, bool) {
	if oldPath, renamed := cm.renames[path]; renamed {
		_, ok := parent.files[oldPath]
		return oldPath, ok
	}
	_, ok := parent.files[path]
	return path, ok
}

func (s *lineState) attribute(ignored map[string]struct{}) Attribution {
	current := s
	for {
		if current.parent != nil {
			current = current.parent
			continue
		}
		if _, isIgnored := ignored[current.introduced.commitID]; !isIgnored {
			return Attribution{
				CommitID: current.introduced.commitID,
				Path:     current.introduced.path,
				Line:     current.introduced.line,
			}
		}
		return Attribution{
			CommitID: current.introduced.commitID,
			Path:     current.introduced.path,
			Line:     current.introduced.line,
			Ignored:  true,
		}
	}
}
