package vcs

import (
	"sort"
	"strings"
	"sync"
)

// ConflictEntry 一个路径的冲突条目，三个阶段各自可缺失。
type ConflictEntry struct {
	Base, Ours, Theirs          []byte
	HasBase, HasOurs, HasTheirs bool
}

func (e ConflictEntry) any() bool { return e.HasBase || e.HasOurs || e.HasTheirs }

// kind 按阶段存在性分派冲突细分，分支互斥且覆盖全部组合。
func (e ConflictEntry) kind() ConflictKind {
	switch {
	case e.HasOurs && e.HasTheirs:
		if e.HasBase {
			return ConflictContent
		}
		return ConflictBothAdded
	case e.HasTheirs: // 本方缺失、对方在
		return ConflictDeletedByUs
	case e.HasOurs: // 对方缺失、本方在
		return ConflictDeletedByThem
	default: // 仅基准
		return ConflictContent
	}
}

// ConflictStages 引入冲突时的三阶段内容，nil 指针表示该阶段缺失。
type ConflictStages struct {
	Base, Ours, Theirs *[]byte
}

// Str 返回指向 s 内容副本的指针，便于构造 ConflictStages。
func Str(s string) *[]byte {
	b := []byte(s)
	return &b
}

// Workspace 版本库工作区：已提交快照、暂存区、工作树三份内容加冲突表。
// 全部导出方法持有同一把互斥锁，任意并发调用等价于某个串行顺序。
type Workspace struct {
	mu        sync.Mutex
	snapshot  map[string][]byte
	index     map[string][]byte
	worktree  map[string][]byte
	conflicts map[string]ConflictEntry
	ignore    *IgnoreSet
	paths     *pathSet

	gen        uint64 // 每次内容变化递增
	cachedGen  uint64
	cacheValid bool
	cache      []PathStatus

	classifies int64 // 分类函数调用次数（性能可验证性）
	commits    int   // 提交序号
}

// New 创建空工作区。ignore 可为 nil 表示无忽略规则。
func New(ignore *IgnoreSet) *Workspace {
	return &Workspace{
		snapshot:  map[string][]byte{},
		index:     map[string][]byte{},
		worktree:  map[string][]byte{},
		conflicts: map[string]ConflictEntry{},
		ignore:    ignore,
		paths:     newPathSet(),
	}
}

// SetIgnore 替换忽略规则（忽略变化会影响未跟踪路径分类，故使缓存失效）。
func (w *Workspace) SetIgnore(s *IgnoreSet) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ignore = s
	w.bump()
}

// Stats 返回分类函数累计调用次数与当前提交序号，供性能与状态断言。
func (w *Workspace) Stats() (classifies int64, commits int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.classifies, w.commits
}

// Unresolved 返回全部未解决冲突路径（有序）。
func (w *Workspace) Unresolved() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return sortedKeys(w.conflicts)
}

func (w *Workspace) bump() {
	w.gen++
	w.cacheValid = false
}

// gc 在路径可能已从全部存储中消失后回收 pathSet 记录。
func (w *Workspace) gc(p string) {
	if _, ok := w.snapshot[p]; ok {
		return
	}
	if _, ok := w.index[p]; ok {
		return
	}
	if _, ok := w.worktree[p]; ok {
		return
	}
	if _, ok := w.conflicts[p]; ok {
		return
	}
	w.paths.remove(p)
}

// WriteFile 模拟外部编辑器写入工作树。
func (w *Workspace) WriteFile(path string, content []byte) error {
	n, err := Normalize(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.paths.has(n) && !w.paths.compatible(n) {
		return errf(CodeInvalidPath, n, "与已存在路径互为祖先后代")
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	w.paths.add(n)
	w.worktree[n] = cp
	w.bump()
	return nil
}

// DeleteFile 模拟外部编辑器删除工作树文件。
func (w *Workspace) DeleteFile(path string) error {
	n, err := Normalize(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.worktree[n]; !ok {
		return errf(CodePathNotExist, n, "工作树中不存在")
	}
	delete(w.worktree, n)
	w.gc(n)
	w.bump()
	return nil
}

// IntroduceConflict 模拟合并引入冲突条目。path 从暂存区移除，
// 阶段内容存入冲突表，工作树不动（由调用方模拟编辑器行为）。
func (w *Workspace) IntroduceConflict(path string, stages ConflictStages) error {
	n, err := Normalize(path)
	if err != nil {
		return err
	}
	entry := ConflictEntry{}
	if stages.Base != nil {
		entry.Base, entry.HasBase = *stages.Base, true
	}
	if stages.Ours != nil {
		entry.Ours, entry.HasOurs = *stages.Ours, true
	}
	if stages.Theirs != nil {
		entry.Theirs, entry.HasTheirs = *stages.Theirs, true
	}
	if !entry.any() {
		return errf(CodeInvalidPath, n, "冲突至少需要一个阶段")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.paths.has(n) && !w.paths.compatible(n) {
		return errf(CodeInvalidPath, n, "与已存在路径互为祖先后代")
	}
	w.paths.add(n)
	delete(w.index, n)
	w.conflicts[n] = entry
	w.bump()
	return nil
}

// resolveBatch 规范化、查重并按 universe（该操作相关的已存在路径集）展开
// 目录前缀。批内重复（规范化后）为参数非法；展开为空为路径不存在。
func (w *Workspace) resolveBatch(raw []string, universe map[string]struct{}) ([]string, *Error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(raw))
	norms := make([]string, 0, len(raw))
	for _, r := range raw {
		n, err := Normalize(r)
		if err != nil {
			return nil, err.(*Error)
		}
		if _, dup := seen[n]; dup {
			return nil, errf(CodeInvalidPath, n, "批内重复路径")
		}
		seen[n] = struct{}{}
		norms = append(norms, n)
	}
	var errs []*Error
	expanded := map[string]struct{}{}
	for _, n := range norms {
		if _, ok := universe[n]; ok {
			expanded[n] = struct{}{}
			continue
		}
		pre := n + "/"
		found := false
		for u := range universe {
			if strings.HasPrefix(u, pre) {
				expanded[u] = struct{}{}
				found = true
			}
		}
		if !found {
			errs = append(errs, errf(CodePathNotExist, n, "路径不存在"))
		}
	}
	if e := prioritize(errs); e != nil {
		return nil, e
	}
	out := make([]string, 0, len(expanded))
	for p := range expanded {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func (w *Workspace) unionOf(maps ...map[string][]byte) map[string]struct{} {
	u := map[string]struct{}{}
	for _, m := range maps {
		for p := range m {
			u[p] = struct{}{}
		}
	}
	return u
}

// Stage 暂存一批路径：工作树内容写入暂存区；工作树不存在而暂存区存在
// 则暂存为删除；冲突路径暂存即解决（以工作树内容为准，缺失则为删除）。
// 整批全有或全无。
func (w *Workspace) Stage(raw []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	universe := w.unionOf(w.worktree, w.index)
	for p := range w.conflicts {
		universe[p] = struct{}{}
	}
	paths, err := w.resolveBatch(raw, universe)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	var errs []*Error
	for _, p := range paths {
		if _, ok := w.conflicts[p]; ok {
			continue
		}
		_, inW := w.worktree[p]
		_, inI := w.index[p]
		if !inW && !inI {
			errs = append(errs, errf(CodePathNotExist, p, "既不在工作树也不在暂存区"))
		}
	}
	if e := prioritize(errs); e != nil {
		return e
	}
	for _, p := range paths {
		if _, ok := w.conflicts[p]; ok {
			delete(w.conflicts, p)
			if c, ok := w.worktree[p]; ok {
				w.index[p] = c
			} else {
				delete(w.index, p)
			}
		} else if c, ok := w.worktree[p]; ok {
			w.index[p] = c
		} else {
			delete(w.index, p)
		}
		w.gc(p)
	}
	w.bump()
	return nil
}

// Unstage 撤销暂存：暂存内容恢复为快照内容（快照没有则从暂存区移除），
// 工作树不动。冲突路径报「冲突中不可撤销」。整批全有或全无。
func (w *Workspace) Unstage(raw []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	universe := w.unionOf(w.index, w.snapshot)
	for p := range w.conflicts {
		universe[p] = struct{}{}
	}
	paths, err := w.resolveBatch(raw, universe)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	var errs []*Error
	for _, p := range paths {
		if _, ok := w.conflicts[p]; ok {
			errs = append(errs, errf(CodeInConflict, p, "冲突中不可撤销"))
			continue
		}
		_, inI := w.index[p]
		_, inS := w.snapshot[p]
		if !inI && !inS {
			errs = append(errs, errf(CodePathNotExist, p, "既不在暂存区也不在快照"))
		}
	}
	if e := prioritize(errs); e != nil {
		return e
	}
	for _, p := range paths {
		if c, ok := w.snapshot[p]; ok {
			w.index[p] = c
		} else {
			delete(w.index, p)
		}
		w.gc(p)
	}
	w.bump()
	return nil
}

// Discard 丢弃工作树修改：工作树恢复为暂存内容，暂存不存在则删除工作树
// 文件。未跟踪路径须 force，否则报「未跟踪需强制」。冲突路径不可直接丢弃，
// 须先暂存解决。整批全有或全无。
func (w *Workspace) Discard(raw []string, force bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	universe := w.unionOf(w.worktree, w.index, w.snapshot)
	for p := range w.conflicts {
		universe[p] = struct{}{}
	}
	paths, err := w.resolveBatch(raw, universe)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	var errs []*Error
	for _, p := range paths {
		if _, ok := w.conflicts[p]; ok {
			errs = append(errs, errf(CodeInConflict, p, "冲突中不可丢弃，请先暂存解决"))
			continue
		}
		_, inW := w.worktree[p]
		_, inI := w.index[p]
		_, inS := w.snapshot[p]
		if !inW && !inI && !inS {
			errs = append(errs, errf(CodePathNotExist, p, "三份内容中均不存在"))
			continue
		}
		if inW && !inI && !inS && !force {
			errs = append(errs, errf(CodeUntrackedNeedsForce, p, "未跟踪需强制"))
		}
	}
	if e := prioritize(errs); e != nil {
		return e
	}
	for _, p := range paths {
		if c, ok := w.index[p]; ok {
			w.worktree[p] = c
		} else {
			delete(w.worktree, p)
		}
		w.gc(p)
	}
	w.bump()
	return nil
}

// Commit 把暂存区固化为新快照。有未解决冲突或（未允许空提交时）暂存区
// 与快照完全相同则失败。返回新的提交序号。
func (w *Workspace) Commit(allowEmpty bool) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.conflicts) > 0 {
		return w.commits, &Error{
			Code:   CodeUnresolvedConflicts,
			Paths:  sortedKeys(w.conflicts),
			Detail: "冲突未解决时不允许提交",
		}
	}
	if !allowEmpty && mapsEqual(w.index, w.snapshot) {
		return w.commits, errf(CodeNothingToCommit, "", "暂存区与快照一致")
	}
	old := w.snapshot
	w.snapshot = cloneMap(w.index)
	w.commits++
	for p := range old {
		w.gc(p)
	}
	w.bump()
	return w.commits, nil
}

// Status 返回全部非「未变更」路径的分类，按路径排序。
// 结果按代缓存：两次调用之间无内容变化时不重新分类任何路径。
func (w *Workspace) Status() []PathStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.cacheValid || w.cachedGen != w.gen {
		union := w.unionOf(w.snapshot, w.index, w.worktree)
		for p := range w.conflicts {
			union[p] = struct{}{}
		}
		list := make([]PathStatus, 0, len(union))
		for p := range union {
			ps := w.classifyLocked(p)
			if ps.Status != StatusUnchanged {
				list = append(list, ps)
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
		w.cache = list
		w.cachedGen = w.gen
		w.cacheValid = true
	}
	out := make([]PathStatus, len(w.cache))
	copy(out, w.cache)
	return out
}

// StatusOf 返回单路径分类。开销为 O(路径深度)，与工作区路径总数无关。
func (w *Workspace) StatusOf(path string) (PathStatus, error) {
	n, err := Normalize(path)
	if err != nil {
		return PathStatus{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.conflicts[n]; !ok {
		_, inS := w.snapshot[n]
		_, inI := w.index[n]
		_, inW := w.worktree[n]
		if !inS && !inI && !inW {
			return PathStatus{}, errf(CodePathNotExist, n, "路径不存在")
		}
	}
	return w.classifyLocked(n), nil
}

func (w *Workspace) classifyLocked(p string) PathStatus {
	w.classifies++
	if e, ok := w.conflicts[p]; ok {
		return PathStatus{Path: p, Status: StatusConflict, Conflict: e.kind()}
	}
	t := triple{}
	t.snap, t.hasSnap = w.snapshot[p]
	t.idx, t.hasIdx = w.index[p]
	t.wt, t.hasWt = w.worktree[p]
	return PathStatus{Path: p, Status: t.classify(w.ignore.Match(p))}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mapsEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || string(va) != string(vb) {
			return false
		}
	}
	return true
}

func cloneMap(m map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}
