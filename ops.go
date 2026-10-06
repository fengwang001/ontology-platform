package statusengine

import (
	"bytes"
	"sort"
)

func (e *Engine) Stage(paths ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	targets, err := e.prepareBatch(paths, e.stageRelevantPaths)
	if err != nil {
		return err
	}
	nextIndex := cloneContent(e.index)
	nextIndexStore := cloneStore(e.indexStore)
	for _, target := range targets {
		if _, worktreeExists := e.worktree[target]; !worktreeExists {
			if _, indexExists := nextIndex[target]; indexExists {
				nextIndexStore.remove(target)
				delete(nextIndex, target)
			} else if _, conflict := e.conflicts[target]; !conflict {
				return &PathError{Path: string(target), Err: ErrPathNotFound}
			}
			continue
		}

		content, exists := e.worktree[target]
		if exists {
			if _, currentlyExists := nextIndex[target]; !currentlyExists {
				if err := nextIndexStore.add(target); err != nil {
					return &PathError{Path: string(target), Err: err}
				}
			}
			nextIndex[target] = cloneBytes(content)
		}
	}
	if err := validateStores(e.snapshotStore, nextIndexStore, e.worktreeStore, e.conflictStore); err != nil {
		return err
	}

	for _, target := range targets {
		if _, conflict := e.conflicts[target]; conflict {
			delete(e.conflicts, target)
			e.conflictStore.remove(target)
		}
	}
	e.index = nextIndex
	e.indexStore = nextIndexStore
	e.changed()
	return nil
}

func (e *Engine) Unstage(paths ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	targets, err := e.prepareBatch(paths, e.unstageRelevantPaths)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if _, conflict := e.conflicts[target]; conflict {
			return &PathError{Path: string(target), Err: ErrConflictUnstage}
		}
	}
	nextIndex := cloneContent(e.index)
	nextIndexStore := cloneStore(e.indexStore)
	for _, target := range targets {
		snapshotContent, inSnapshot := e.snapshot[target]
		if inSnapshot {
			if _, exists := nextIndex[target]; !exists {
				if err := nextIndexStore.add(target); err != nil {
					return &PathError{Path: string(target), Err: err}
				}
			}
			nextIndex[target] = cloneBytes(snapshotContent)
		} else if _, exists := nextIndex[target]; exists {
			nextIndexStore.remove(target)
			delete(nextIndex, target)
		}
	}
	if err := validateStores(e.snapshotStore, nextIndexStore, e.worktreeStore, e.conflictStore); err != nil {
		return err
	}
	e.index = nextIndex
	e.indexStore = nextIndexStore
	e.changed()
	return nil
}

func (e *Engine) Discard(force bool, paths ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	targets, err := e.prepareBatch(paths, e.discardRelevantPaths)
	if err != nil {
		return err
	}
	for _, target := range targets {
		entry := e.classify(target)
		if (entry.Status == StatusUntracked || entry.Status == StatusIgnored) && !force {
			return &PathError{Path: string(target), Err: ErrUntrackedNeedsForce}
		}
	}

	nextWorktree := cloneContent(e.worktree)
	nextWorktreeStore := cloneStore(e.worktreeStore)
	for _, target := range targets {
		content, inIndex := e.index[target]
		if inIndex {
			if _, exists := nextWorktree[target]; !exists {
				if err := nextWorktreeStore.add(target); err != nil {
					return &PathError{Path: string(target), Err: err}
				}
			}
			nextWorktree[target] = cloneBytes(content)
		} else if _, exists := nextWorktree[target]; exists {
			nextWorktreeStore.remove(target)
			delete(nextWorktree, target)
		}
	}
	if err := validateStores(e.snapshotStore, e.indexStore, nextWorktreeStore, e.conflictStore); err != nil {
		return err
	}
	e.worktree = nextWorktree
	e.worktreeStore = nextWorktreeStore
	e.changed()
	return nil
}

func (e *Engine) Commit(allowEmpty bool) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.conflicts) > 0 {
		return 0, &PathError{Path: e.sortedConflictPaths()[0], Err: ErrUnresolvedConflicts}
	}
	if !allowEmpty && contentMapsEqual(e.snapshot, e.index) {
		return 0, ErrNothingToCommit
	}
	e.snapshot = cloneContent(e.index)
	e.snapshotStore = cloneStore(e.indexStore)
	e.commitID++
	e.changed()
	return e.commitID, nil
}

func (e *Engine) BeginMerge(entries map[string]Stages) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.conflicts) > 0 {
		path := e.sortedConflictPaths()[0]
		return &PathError{Path: path, Err: ErrUnresolvedConflicts}
	}
	rawPaths := make([]string, 0, len(entries))
	for path := range entries {
		rawPaths = append(rawPaths, path)
	}
	sort.Strings(rawPaths)

	normalizedEntries := make(map[normalizedPath]Stages, len(entries))
	nextConflictStore := newStore()
	nextIndex := cloneContent(e.index)
	nextIndexStore := cloneStore(e.indexStore)
	for _, rawPath := range rawPaths {
		path, err := normalizePath(rawPath)
		if err != nil {
			return &PathError{Path: rawPath, Err: err}
		}
		stages := entries[rawPath]
		if stages.Base == nil && stages.Ours == nil && stages.Theirs == nil {
			return &PathError{Path: rawPath, Err: ErrInvalidPath}
		}
		if _, duplicate := normalizedEntries[path]; duplicate {
			return &PathError{Path: rawPath, Err: ErrInvalidPath}
		}
		if err := nextConflictStore.add(path); err != nil {
			return &PathError{Path: rawPath, Err: err}
		}
		normalizedEntries[path] = Stages{
			Base:   cloneBytes(stages.Base),
			Ours:   cloneBytes(stages.Ours),
			Theirs: cloneBytes(stages.Theirs),
		}
		if _, exists := nextIndex[path]; exists {
			nextIndexStore.remove(path)
			delete(nextIndex, path)
		}
	}
	if err := validateStores(e.snapshotStore, nextIndexStore, e.worktreeStore, nextConflictStore); err != nil {
		return err
	}
	e.conflicts = normalizedEntries
	e.conflictStore = nextConflictStore
	e.index = nextIndex
	e.indexStore = nextIndexStore
	e.changed()
	return nil
}

func (e *Engine) UnresolvedConflicts() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sortedConflictPaths()
}

type relevantPathsFunc func(prefix normalizedPath) []normalizedPath

func (e *Engine) prepareBatch(rawPaths []string, relevant relevantPathsFunc) ([]normalizedPath, error) {
	normalizedInputs := make([]normalizedPath, 0, len(rawPaths))
	seenInputs := map[normalizedPath]struct{}{}
	for _, rawPath := range rawPaths {
		path, err := normalizePath(rawPath)
		if err != nil {
			return nil, &PathError{Path: rawPath, Err: err}
		}
		if _, duplicate := seenInputs[path]; duplicate {
			return nil, &PathError{Path: rawPath, Err: ErrInvalidPath}
		}
		seenInputs[path] = struct{}{}
		normalizedInputs = append(normalizedInputs, path)
	}

	targets := map[normalizedPath]struct{}{}
	for _, input := range normalizedInputs {
		matched := relevant(input)
		if len(matched) == 0 {
			return nil, &PathError{Path: string(input), Err: ErrPathNotFound}
		}
		for _, target := range matched {
			if _, duplicate := targets[target]; duplicate {
				return nil, &PathError{Path: string(target), Err: ErrInvalidPath}
			}
			targets[target] = struct{}{}
		}
	}
	return sortedPathSet(targets), nil
}

func (e *Engine) stageRelevantPaths(prefix normalizedPath) []normalizedPath {
	result := map[normalizedPath]struct{}{}
	addRelevantKeys(prefix, e.worktree, result)
	addRelevantKeys(prefix, e.index, result)
	addRelevantKeys(prefix, e.conflicts, result)
	return sortedPathSet(result)
}

func (e *Engine) unstageRelevantPaths(prefix normalizedPath) []normalizedPath {
	result := map[normalizedPath]struct{}{}
	addRelevantKeys(prefix, e.snapshot, result)
	addRelevantKeys(prefix, e.index, result)
	addRelevantKeys(prefix, e.conflicts, result)
	return sortedPathSet(result)
}

func (e *Engine) discardRelevantPaths(prefix normalizedPath) []normalizedPath {
	result := map[normalizedPath]struct{}{}
	addRelevantKeys(prefix, e.worktree, result)
	addRelevantKeys(prefix, e.index, result)
	addRelevantKeys(prefix, e.conflicts, result)
	return sortedPathSet(result)
}

func addRelevantKeys[T any](prefix normalizedPath, source map[normalizedPath]T, result map[normalizedPath]struct{}) {
	for path := range source {
		if isDescendantOrEqual(path, prefix) {
			result[path] = struct{}{}
		}
	}
}

func validateStores(stores ...engineStore) error {
	combinedFiles := map[normalizedPath]struct{}{}
	for _, store := range stores {
		for path := range store.fileRef {
			combinedFiles[path] = struct{}{}
		}
	}
	for path := range combinedFiles {
		for parent := path.Dir(); parent != "."; parent = parent.Dir() {
			if _, conflict := combinedFiles[parent]; conflict {
				return &PathError{Path: string(path), Err: ErrInvalidPath}
			}
		}
	}
	return nil
}

func contentMapsEqual(left, right contentMap) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftValue := range left {
		rightValue, exists := right[path]
		if !exists || !bytes.Equal(leftValue, rightValue) {
			return false
		}
	}
	return true
}

func sortedPathSet(paths map[normalizedPath]struct{}) []normalizedPath {
	result := make([]normalizedPath, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (e *Engine) sortedConflictPaths() []string {
	paths := make([]normalizedPath, 0, len(e.conflicts))
	for path := range e.conflicts {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i] < paths[j] })
	result := make([]string, len(paths))
	for i, path := range paths {
		result[i] = string(path)
	}
	return result
}
