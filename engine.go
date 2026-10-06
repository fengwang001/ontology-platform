package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// PathStatus 是查询接口的五种互斥结果。
type PathStatus int

const (
	StatusMaterialized PathStatus = iota + 1
	StatusExcludedByRule
	StatusNoRuleMatch
	StatusEmptyDirectory
	StatusNotInCommit
)

// Diff 是一次变更后物化集合的三类差集。
// Kept 采用惰性口径：只列「受影响面内仍保持物化」的路径，
// 未受影响的已物化路径不会被遍历（见 DESIGN.md 性能取舍）。
type Diff struct {
	Added   []string
	Removed []string
	Kept    []string
}

// BlockedError 携带受阻路径完整列表（按字节序、去重）。
type BlockedError struct {
	Paths []string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("blocked by local modifications on %d path(s)", len(e.Paths))
}

// 哨兵错误
var (
	ErrInvalidRules    = errors.New("invalid rule set")
	ErrCommitNotFound  = errors.New("commit not found")
	ErrRulesetNotFound = errors.New("ruleset version not found")
)

// engineState 是引擎的全部可变状态，整体受 rw 保护。
type engineState struct {
	commitID  string
	commit    *Commit
	version   int
	rules     *compiled
	matFiles  map[string]bool // 已物化文件
	matDirs   map[string]bool // 已物化目录（不含根）
	dirCount  map[string]int  // 每个目录下已物化文件数（用于增量维护目录物化）
	rootMat   bool
	dirty     map[string]bool // 本地修改标记（可覆盖文件与目录）
	commits   map[string]*Commit
	versions  map[int]*compiled
	emptyTree *trieNode
}

// Engine 是稀疏检出规则引擎；所有方法并发安全，
// 互斥锁保证变更与查询等价于某个串行顺序，查询不会看到中间状态。
type Engine struct {
	mu sync.RWMutex
	st engineState
}

// NewEngine 创建引擎，初始为空提交、规则集版本 0（空规则集）。
func NewEngine() *Engine {
	empty, _ := buildCommit("", nil)
	e := &Engine{}
	s := &e.st
	s.commits = map[string]*Commit{"": empty}
	s.versions = map[int]*compiled{}
	s.commitID = ""
	s.commit = empty
	s.version = 0
	s.rules, _ = validateAndCompile(nil)
	s.matFiles = map[string]bool{}
	s.matDirs = map[string]bool{}
	s.dirCount = map[string]int{}
	s.dirty = map[string]bool{}
	s.emptyTree = empty.root
	return e
}

// AddCommit 注册一次提交（文件路径列表）。
func (e *Engine) AddCommit(id string, files []string) error {
	c, err := buildCommit(id, files)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return errors.New("commit id must not be empty")
	}
	e.st.commits[id] = c
	return nil
}

// ReplaceRules 校验并替换规则集，产生严格递增的新版本；
// commitID != "" 时同时切换提交。裁决是整体的。
// force=true 时丢弃受阻路径上的本地修改并在 discarded 中列出。
func (e *Engine) ReplaceRules(rules []Rule, commitID string, force bool) (newVersion int, diff Diff, discarded []string, err error) {
	compiled, err := validateAndCompile(rules)
	if err != nil {
		return 0, Diff{}, nil, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	targetCommit := e.st.commit
	if commitID != "" {
		c, ok := e.st.commits[commitID]
		if !ok {
			return 0, Diff{}, nil, ErrCommitNotFound
		}
		targetCommit = c
	}
	newVer := e.st.version + 1
	e.st.versions[newVer] = compiled
	diff, discarded, err = e.applyLocked(targetCommit, newVer, compiled, force)
	if err != nil {
		delete(e.st.versions, newVer)
		return 0, Diff{}, nil, err
	}
	if commitID != "" {
		e.st.commitID = commitID
		e.st.commit = targetCommit
	}
	e.st.version = newVer
	e.st.rules = compiled
	return newVer, diff, discarded, nil
}

// ApplyRuleset 仅切换提交或仅应用已注册的规则集版本；
// commitID=="" 表示不换提交，version<=0 表示不换规则集。
func (e *Engine) ApplyRuleset(commitID string, version int, force bool) (diff Diff, discarded []string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	targetCommit := e.st.commit
	if commitID != "" {
		c, ok := e.st.commits[commitID]
		if !ok {
			return Diff{}, nil, ErrCommitNotFound
		}
		targetCommit = c
	}
	targetRules := e.st.rules
	targetVersion := e.st.version
	if version > 0 {
		c, ok := e.st.versions[version]
		if !ok {
			return Diff{}, nil, ErrRulesetNotFound
		}
		targetRules = c
		targetVersion = version
	}
	diff, discarded, err = e.applyLocked(targetCommit, targetVersion, targetRules, force)
	if err != nil {
		return Diff{}, nil, err
	}
	if commitID != "" {
		e.st.commitID = commitID
		e.st.commit = targetCommit
	}
	e.st.version = targetVersion
	e.st.rules = targetRules
	return diff, discarded, nil
}

// applyLocked 在持锁状态下计算并实施目标状态；受阻时不改动任何状态。
func (e *Engine) applyLocked(targetCommit *Commit, targetVersion int, targetRules *compiled, force bool) (Diff, []string, error) {
	s := &e.st
	entries := affectedFiles(s.commit.root, targetCommit.root, s.rules, targetRules)

	oldOn := map[string]bool{}
	newOn := map[string]bool{}
	for _, en := range entries {
		if en.oldSeen && en.oldV == verdictIncluded {
			oldOn[en.path] = true
		}
		if en.newSeen && en.newV == verdictIncluded {
			newOn[en.path] = true
		}
	}

	added, removed, kept := []string{}, []string{}, []string{}
	for p := range newOn {
		if !oldOn[p] {
			added = append(added, p)
		} else {
			kept = append(kept, p)
		}
	}
	for p := range oldOn {
		if !newOn[p] {
			removed = append(removed, p)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(kept)

	// 撤销物化 = 旧物化 - 新物化。oldOn 只含受影响面内的旧包含文件，
	// 受影响面外的旧物化文件必然仍在新集合中（剪枝保证）。
	blockedSet := map[string]bool{}
	for _, p := range removed {
		if s.dirty[p] {
			blockedSet[p] = true
		}
	}
	if len(blockedSet) > 0 && !force {
		blocked := make([]string, 0, len(blockedSet))
		for p := range blockedSet {
			blocked = append(blocked, p)
		}
		sort.Strings(blocked)
		return Diff{}, nil, &BlockedError{Paths: blocked}
	}
	discarded := []string{}
	if force {
		for p := range blockedSet {
			discarded = append(discarded, p)
		}
		sort.Strings(discarded)
	}

	// 提交切换中「目标提交不存在的已物化文件」已由 affectedFiles 覆盖
	// （oldSeen=true/newSeen=false 且旧裁决 Include）。
	// 变更已在持锁临界区、且通过受阻检查后才执行，无需复制整个物化集合；
	// 只对 added/removed 触及的路径与祖先计数做增量更新。
	for _, p := range removed {
		delete(s.matFiles, p)
		s.decAncestors(p)
		if force {
			delete(s.dirty, p)
		}
	}
	for _, p := range added {
		s.matFiles[p] = true
		s.incAncestors(p)
	}
	s.rootMat = len(s.matFiles) > 0
	return Diff{Added: added, Removed: removed, Kept: kept}, discarded, nil
}

// incAncestors 为一个新物化文件的各祖先目录递增计数；
// 计数从 0 变 1 时该目录才进入物化集合。
func (s *engineState) incAncestors(file string) {
	segs := splitPath(file)
	for i := 1; i < len(segs); i++ {
		d := joinSegs(segs[:i])
		s.dirCount[d]++
		if s.dirCount[d] == 1 {
			s.matDirs[d] = true
		}
	}
}

// decAncestors 为一个撤销物化文件的各祖先目录递减计数；
// 计数归零时该目录退出物化集合（空目录不物化）。
func (s *engineState) decAncestors(file string) {
	segs := splitPath(file)
	for i := 1; i < len(segs); i++ {
		d := joinSegs(segs[:i])
		s.dirCount[d]--
		if s.dirCount[d] <= 0 {
			delete(s.dirCount, d)
			delete(s.matDirs, d)
		}
	}
}

// MarkDirty 在已物化路径上设置本地修改标记。
func (e *Engine) MarkDirty(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.isMaterializedLocked(path) {
		return fmt.Errorf("path is not materialized: %q", path)
	}
	e.st.dirty[path] = true
	return nil
}

// ClearDirty 清除本地修改标记；不存在标记时为空操作。
func (e *Engine) ClearDirty(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.st.dirty, path)
	return nil
}

func (e *Engine) isMaterializedLocked(path string) bool {
	if path == "" {
		return e.st.rootMat
	}
	if e.st.matFiles[path] {
		return true
	}
	return e.st.matDirs[path]
}

// IsDirty 查询某路径是否带本地修改标记（测试与外部观察用）。
func (e *Engine) IsDirty(path string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.st.dirty[path]
}

// QueryPath 返回路径的五种互斥状态之一。
func (e *Engine) QueryPath(path string) PathStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := &e.st
	segs := splitPath(path)
	node := findNode(s.commit.root, segs)
	if node == nil {
		return StatusNotInCommit
	}
	if e.isMaterializedLocked(path) {
		return StatusMaterialized
	}
	v := pathVerdict(s.rules, segs)
	if v == verdictExcluded {
		return StatusExcludedByRule
	}
	if node.isFile {
		return StatusNoRuleMatch
	}
	// 目录：存在一条最终裁决为包含的自身/后代路径，却无物化后代 => 空目录。
	rt := buildRuleTrie(s.rules)
	if subtreeMayInclude(s.rules, rt, s.commit.root, segs) {
		return StatusEmptyDirectory
	}
	return StatusNoRuleMatch
}

// ListMaterialized 按字节序列出给定前缀下所有物化路径（文件与目录）。
func (e *Engine) ListMaterialized(prefix string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []string
	if prefix == "" {
		for p := range e.st.matFiles {
			out = append(out, p)
		}
		for d := range e.st.matDirs {
			out = append(out, d)
		}
	} else {
		pfx := prefix + "/"
		for p := range e.st.matFiles {
			if p == prefix || hasPrefix(p, pfx) {
				out = append(out, p)
			}
		}
		for d := range e.st.matDirs {
			if d == prefix || hasPrefix(d, pfx) {
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

func hasPrefix(s, pfx string) bool {
	return len(s) >= len(pfx) && s[:len(pfx)] == pfx
}

// CurrentCommit / CurrentVersion 返回当前状态。
func (e *Engine) CurrentCommit() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.st.commitID
}

func (e *Engine) CurrentVersion() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.st.version
}

// CurrentState 在同一临界区内返回 (提交ID, 规则集版本) 与当前物化集合快照，
// 保证三者必然对应同一次串行状态。
func (e *Engine) CurrentState() (commitID string, version int, materialized []string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	materialized = make([]string, 0, len(e.st.matFiles)+len(e.st.matDirs))
	for p := range e.st.matFiles {
		materialized = append(materialized, p)
	}
	for p := range e.st.matDirs {
		materialized = append(materialized, p)
	}
	sort.Strings(materialized)
	return e.st.commitID, e.st.version, materialized
}
