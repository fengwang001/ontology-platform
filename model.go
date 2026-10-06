package statusengine

import (
	"bytes"
	"errors"
	"sync"
)

var (
	ErrInvalidPath         = errors.New("参数非法")
	ErrPathNotFound        = errors.New("路径不存在")
	ErrConflictUnstage     = errors.New("冲突中不可撤销")
	ErrUntrackedNeedsForce = errors.New("未跟踪需强制")
	ErrUnresolvedConflicts = errors.New("存在未解决冲突")
	ErrNothingToCommit     = errors.New("无可提交内容")
)

type Status int

const (
	StatusUnchanged Status = iota
	StatusStagedAdded
	StatusStagedModified
	StatusStagedDeleted
	StatusWorktreeModified
	StatusWorktreeDeleted
	StatusUntracked
	StatusStagedThenModified
	StatusStagedDeletedThenRecreated
	StatusIgnored
	StatusConflict
)

type ConflictKind int

const (
	ConflictContent ConflictKind = iota
	ConflictOursDeletedTheirsModified
	ConflictOursModifiedTheirsDeleted
	ConflictBothAdded
)

type Entry struct {
	Path         string
	Status       Status
	ConflictKind ConflictKind
}

type Stages struct {
	Base   []byte
	Ours   []byte
	Theirs []byte
}

type Snapshot map[string][]byte

type IgnoreFunc func(path string) bool

type contentMap map[normalizedPath][]byte

type engineStore struct {
	content contentMap
	fileRef map[normalizedPath]int
	dirRef  map[normalizedPath]int
}

type Engine struct {
	mu            sync.RWMutex
	snapshot      contentMap
	index         contentMap
	worktree      contentMap
	conflicts     map[normalizedPath]Stages
	snapshotStore engineStore
	indexStore    engineStore
	worktreeStore engineStore
	conflictStore engineStore
	ignored       IgnoreFunc
	commitID      int
	listCache     []Entry
	listVersion   uint64
	version       uint64
}

func NewEngine(snapshot Snapshot, ignored IgnoreFunc) (*Engine, error) {
	store := engineStore{
		content: contentMap{},
		fileRef: map[normalizedPath]int{},
		dirRef:  map[normalizedPath]int{},
	}
	for rawPath, content := range snapshot {
		path, err := normalizePath(rawPath)
		if err != nil {
			return nil, &PathError{Path: rawPath, Err: err}
		}
		if _, exists := store.content[path]; exists {
			return nil, &PathError{Path: rawPath, Err: ErrInvalidPath}
		}
		if err := store.add(path); err != nil {
			return nil, &PathError{Path: rawPath, Err: err}
		}
		store.content[path] = cloneBytes(content)
	}
	initial := cloneContent(store.content)
	return &Engine{
		snapshot:      initial,
		index:         cloneContent(initial),
		worktree:      cloneContent(initial),
		conflicts:     map[normalizedPath]Stages{},
		snapshotStore: cloneStore(store),
		indexStore:    cloneStore(store),
		worktreeStore: cloneStore(store),
		conflictStore: newStore(),
		ignored:       ignored,
	}, nil
}

type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }

func newStore() engineStore {
	return engineStore{
		content: contentMap{},
		fileRef: map[normalizedPath]int{},
		dirRef:  map[normalizedPath]int{},
	}
}

func (s *engineStore) add(path normalizedPath) error {
	if s.fileRef[path] > 0 || s.dirRef[path] > 0 {
		return ErrInvalidPath
	}
	s.fileRef[path]++
	parent := normalizedPath(path.Dir())
	for parent != "." {
		if s.fileRef[parent] > 0 {
			return ErrInvalidPath
		}
		s.dirRef[parent]++
		parent = parent.Dir()
	}
	return nil
}

func (s *engineStore) remove(path normalizedPath) {
	s.fileRef[path]--
	if s.fileRef[path] == 0 {
		delete(s.fileRef, path)
	}
	parent := path.Dir()
	for parent != "." {
		s.dirRef[parent]--
		if s.dirRef[parent] == 0 {
			delete(s.dirRef, parent)
		}
		next := parent.Dir()
		if next == parent {
			break
		}
		parent = next
	}
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

func cloneContent(source contentMap) contentMap {
	result := make(contentMap, len(source))
	for path, value := range source {
		result[path] = cloneBytes(value)
	}
	return result
}

func cloneStore(source engineStore) engineStore {
	return engineStore{
		content: cloneContent(source.content),
		fileRef: cloneIntMap(source.fileRef),
		dirRef:  cloneIntMap(source.dirRef),
	}
}

func cloneIntMap(source map[normalizedPath]int) map[normalizedPath]int {
	result := make(map[normalizedPath]int, len(source))
	for path, value := range source {
		result[path] = value
	}
	return result
}

func bytesEqual(left, right []byte, existsLeft, existsRight bool) bool {
	if !existsLeft || !existsRight {
		return existsLeft == existsRight
	}
	return bytes.Equal(left, right)
}
