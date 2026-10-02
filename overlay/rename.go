package overlay

import (
	"sort"
	"strings"
)

// Rename 把 old 改名为 new。old 的内容有任何部分来自下层目录时
// 拒绝（跨层）；其余情形按文件或纯上层目录处理。所有「下层可达」
// 判定均按改名前的状态。
func (fs *FS) Rename(oldPath, newPath string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	// old 的路径与祖先判定（先于 new）。
	if !validOpPath(oldPath, false) {
		return ErrInvalidPath
	}
	if err := fs.checkAncestors(oldPath); err != nil {
		return err
	}
	oldEntry, ok := fs.lookup(oldPath)
	if !ok {
		return ErrNotFound
	}
	// new 的路径与祖先判定。
	if !validOpPath(newPath, false) {
		return ErrInvalidPath
	}
	if err := fs.checkAncestors(newPath); err != nil {
		return err
	}
	// 移入自身。
	if newPath == oldPath || strings.HasPrefix(newPath, oldPath+"/") {
		return ErrRenameSelf
	}
	// 跨层判定：old 的内容有任何部分来自下层目录时拒绝。
	oldUpper, hasOldUpper := fs.upper[oldPath]
	oldLowerReachable := fs.lowerReachable(oldPath)
	if !hasOldUpper && oldLowerReachable && fs.lower[oldPath].isDir {
		return ErrCrossLayer
	}
	if hasOldUpper && oldUpper.kind == recDir &&
		oldLowerReachable && fs.lower[oldPath].isDir {
		return ErrCrossLayer
	}
	// new 的冲突判定（白障视为不存在）。
	newEntry, newExists := fs.lookup(newPath)
	if newExists {
		switch {
		case oldEntry.Type == EntryFile && newEntry.Type == EntryDir:
			return ErrIsDir
		case oldEntry.Type == EntryDir && newEntry.Type == EntryFile:
			return ErrNotDir
		case oldEntry.Type == EntryDir && newEntry.Type == EntryDir:
			if len(fs.readDirLocked(newPath)) > 0 {
				return ErrNotEmpty
			}
		}
	}
	if oldEntry.Type == EntryFile {
		fs.renameFileLocked(oldPath, newPath, oldEntry.Content)
		return nil
	}
	fs.renameDirLocked(oldPath, newPath, oldUpper.kind, oldLowerReachable)
	return nil
}

// renameFileLocked 实现文件改名：先按 Write(new, 内容) 执行，再按
// Remove(old) 执行。调用方须持有写锁且前置判定已通过。
func (fs *FS) renameFileLocked(oldPath, newPath string, content []byte) {
	fs.ensureAncestors(newPath)
	fs.upper[newPath] = record{kind: recFile, content: append([]byte(nil), content...)}
	if fs.lowerReachable(oldPath) {
		fs.ensureAncestors(oldPath)
		fs.dropSubtree(oldPath)
		fs.upper[oldPath] = record{kind: recWhiteout}
	} else {
		fs.dropSubtree(oldPath)
	}
}

// renameDirLocked 实现纯上层目录改名。调用方须持有写锁且前置判定
// 已通过；oldKind 是 U(old) 的种类（普通或不透明目录）。
func (fs *FS) renameDirLocked(oldPath, newPath string, oldKind recKind, oldLowerReachable bool) {
	// 以下判定一律使用改名前的状态。
	_, hasNewUpper := fs.upper[newPath]
	newIsWhiteout := hasNewUpper && fs.upper[newPath].kind == recWhiteout
	newLowerDir := false
	if e, ok := fs.lower[newPath]; ok && e.isDir && fs.lowerReachable(newPath) {
		newLowerDir = true
	}
	// 丢弃上层 new 及其之下的全部记录。
	fs.dropSubtree(newPath)
	// 补 new 缺失的上层祖先为普通目录。
	fs.ensureAncestors(newPath)
	// 把上层 old 及其之下的全部记录整体改前缀为 new。
	oldPrefix := oldPath + "/"
	var moved []string
	for q := range fs.upper {
		if q == oldPath || strings.HasPrefix(q, oldPrefix) {
			moved = append(moved, q)
		}
	}
	sort.Strings(moved)
	for _, q := range moved {
		r := fs.upper[q]
		delete(fs.upper, q)
		fs.upper[newPath+q[len(oldPath):]] = r
	}
	// new 处的记录为不透明目录，当且仅当 old 的记录是不透明目录、
	// 或 new 原有的上层记录是白障、或 L(new) 是目录且 new 下层可达。
	rec := fs.upper[newPath]
	if oldKind == recOpaque || newIsWhiteout || newLowerDir {
		rec.kind = recOpaque
	}
	fs.upper[newPath] = rec
	// 原处处理：old 下层可达则补祖先并写白障，否则不留记录。
	if oldLowerReachable {
		fs.ensureAncestors(oldPath)
		fs.upper[oldPath] = record{kind: recWhiteout}
	}
}
