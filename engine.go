package statusengine

import (
	"bytes"
	"sort"
)

var statusNames = [...]string{
	"未变更",
	"已暂存新增",
	"已暂存修改",
	"已暂存删除",
	"工作树修改",
	"工作树删除",
	"未跟踪",
	"暂存后又修改",
	"暂存删除后又重建",
	"被忽略",
	"冲突",
}

func (s Status) String() string {
	if int(s) < 0 || int(s) >= len(statusNames) {
		return "未知状态"
	}
	return statusNames[s]
}

func (e *Engine) Status(path string) (Entry, error) {
	normalized, err := normalizePath(path)
	if err != nil {
		return Entry{}, &PathError{Path: path, Err: err}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	_, inSnapshot := e.snapshot[normalized]
	_, inIndex := e.index[normalized]
	_, inWorktree := e.worktree[normalized]
	_, inConflict := e.conflicts[normalized]
	if !inSnapshot && !inIndex && !inWorktree && !inConflict {
		return Entry{}, &PathError{Path: path, Err: ErrPathNotFound}
	}
	return e.classify(normalized), nil
}

func (e *Engine) List() []Entry {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listVersion == e.version && e.listCache != nil {
		return e.listCache
	}

	paths := make(map[normalizedPath]struct{}, len(e.snapshot)+len(e.index)+len(e.worktree)+len(e.conflicts))
	for path := range e.snapshot {
		paths[path] = struct{}{}
	}
	for path := range e.index {
		paths[path] = struct{}{}
	}
	for path := range e.worktree {
		paths[path] = struct{}{}
	}
	for path := range e.conflicts {
		paths[path] = struct{}{}
	}

	entries := make([]Entry, 0, len(paths))
	for path := range paths {
		entries = append(entries, e.classify(path))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	e.listCache = entries
	e.listVersion = e.version
	return entries
}

func (e *Engine) CommitID() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.commitID
}

func (e *Engine) SnapshotPath(path string) ([]byte, bool, error) {
	return e.readContent(path, e.snapshot)
}

func (e *Engine) IndexPath(path string) ([]byte, bool, error) {
	return e.readContent(path, e.index)
}

func (e *Engine) WorktreePath(path string) ([]byte, bool, error) {
	return e.readContent(path, e.worktree)
}

func (e *Engine) WriteWorktree(path string, content []byte) error {
	normalized, err := normalizePath(path)
	if err != nil {
		return &PathError{Path: path, Err: err}
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.worktree[normalized]; exists {
		e.worktree[normalized] = cloneBytes(content)
		e.changed()
		return nil
	}
	if !e.canUsePath(normalized) {
		return &PathError{Path: path, Err: ErrInvalidPath}
	}
	if err := e.worktreeStore.add(normalized); err != nil {
		return &PathError{Path: path, Err: err}
	}
	e.worktree[normalized] = cloneBytes(content)
	e.changed()
	return nil
}

func (e *Engine) RemoveWorktree(path string) error {
	normalized, err := normalizePath(path)
	if err != nil {
		return &PathError{Path: path, Err: err}
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.worktree[normalized]; !exists {
		return &PathError{Path: path, Err: ErrPathNotFound}
	}
	e.worktreeStore.remove(normalized)
	delete(e.worktree, normalized)
	e.changed()
	return nil
}

func (e *Engine) readContent(path string, source contentMap) ([]byte, bool, error) {
	normalized, err := normalizePath(path)
	if err != nil {
		return nil, false, &PathError{Path: path, Err: err}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	content, exists := source[normalized]
	return cloneBytes(content), exists, nil
}

func (e *Engine) classify(path normalizedPath) Entry {
	entry := Entry{Path: string(path)}
	if stages, conflict := e.conflicts[path]; conflict {
		entry.Status = StatusConflict
		entry.ConflictKind = classifyConflict(stages)
		return entry
	}

	snapshotContent, inSnapshot := e.snapshot[path]
	indexContent, inIndex := e.index[path]
	worktreeContent, inWorktree := e.worktree[path]
	snapshotEqualsIndex := bytesEqual(snapshotContent, indexContent, inSnapshot, inIndex)
	indexEqualsWorktree := bytesEqual(indexContent, worktreeContent, inIndex, inWorktree)

	switch {
	case snapshotEqualsIndex && indexEqualsWorktree:
		entry.Status = StatusUnchanged
	case snapshotEqualsIndex && inSnapshot && inIndex && !inWorktree:
		entry.Status = StatusWorktreeDeleted
	case snapshotEqualsIndex && inSnapshot && inIndex && !bytes.Equal(worktreeContent, indexContent):
		entry.Status = StatusWorktreeModified
	case snapshotEqualsIndex && !inSnapshot && !inIndex && inWorktree:
		entry.Status = StatusUntracked
		if e.ignored != nil && e.ignored(string(path)) {
			entry.Status = StatusIgnored
		}
	case !inIndex && inSnapshot && inWorktree:
		entry.Status = StatusStagedDeletedThenRecreated
	case !snapshotEqualsIndex && !inIndex && inSnapshot && !inWorktree:
		entry.Status = StatusStagedDeleted
	case inIndex && !inSnapshot && indexEqualsWorktree:
		entry.Status = StatusStagedAdded
	case inIndex && inSnapshot && indexEqualsWorktree:
		entry.Status = StatusStagedModified
	default:
		entry.Status = StatusStagedThenModified
	}
	return entry
}

func classifyConflict(stages Stages) ConflictKind {
	hasBase := stages.Base != nil
	hasOurs := stages.Ours != nil
	hasTheirs := stages.Theirs != nil
	switch {
	case hasBase && hasOurs && hasTheirs:
		return ConflictContent
	case !hasBase && hasOurs && hasTheirs:
		return ConflictBothAdded
	case !hasOurs:
		return ConflictOursDeletedTheirsModified
	case !hasTheirs:
		return ConflictOursModifiedTheirsDeleted
	default:
		return ConflictContent
	}
}

func (e *Engine) canUsePath(path normalizedPath) bool {
	return storeAllows(e.snapshotStore, path) &&
		storeAllows(e.indexStore, path) &&
		storeAllows(e.worktreeStore, path) &&
		storeAllows(e.conflictStore, path)
}

func storeAllows(store engineStore, path normalizedPath) bool {
	if store.fileRef[path] > 0 {
		return true
	}
	if store.dirRef[path] > 0 {
		return false
	}
	for parent := path.Dir(); parent != "."; parent = parent.Dir() {
		if store.fileRef[parent] > 0 {
			return false
		}
	}
	return true
}

func (e *Engine) changed() {
	e.version++
	e.listCache = nil
	e.listVersion = 0
}
