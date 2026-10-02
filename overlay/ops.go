package overlay

import "strings"

// ensureAncestors 把 p 缺失的上层祖先记录补为普通目录（自根向下）。
// 调用方须持有写锁，且祖先在合并视图中均须已解析为目录。
func (fs *FS) ensureAncestors(p string) {
	for _, a := range ancestors(p) {
		if _, ok := fs.upper[a]; !ok {
			fs.upper[a] = record{kind: recDir}
		}
	}
}

// dropSubtree 丢弃上层 p 及其之下的全部记录。调用方须持有写锁。
func (fs *FS) dropSubtree(p string) {
	prefix := p + "/"
	for q := range fs.upper {
		if q == p || strings.HasPrefix(q, prefix) {
			delete(fs.upper, q)
		}
	}
}

// Mkdir 在合并视图中创建目录。path 已存在则拒绝；缺失的上层祖先
// 记录补为普通目录；U(path) 是白障则替换为不透明目录，否则记为
// 普通目录。
func (fs *FS) Mkdir(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !validOpPath(path, false) {
		return ErrInvalidPath
	}
	if err := fs.checkAncestors(path); err != nil {
		return err
	}
	if _, ok := fs.lookup(path); ok {
		return ErrExist
	}
	fs.ensureAncestors(path)
	if r, ok := fs.upper[path]; ok && r.kind == recWhiteout {
		fs.upper[path] = record{kind: recOpaque}
	} else {
		fs.upper[path] = record{kind: recDir}
	}
	return nil
}

// Write 在合并视图中写入文件。path 是目录则拒绝；缺失的上层祖先
// 记录补为普通目录；U(path) 记为文件（替换白障或旧文件）。
func (fs *FS) Write(path string, content []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !validOpPath(path, false) {
		return ErrInvalidPath
	}
	if err := fs.checkAncestors(path); err != nil {
		return err
	}
	if e, ok := fs.lookup(path); ok && e.Type == EntryDir {
		return ErrIsDir
	}
	fs.ensureAncestors(path)
	fs.upper[path] = record{kind: recFile, content: append([]byte(nil), content...)}
	return nil
}

// Remove 删除合并视图中的文件或空目录。path 下层可达时先补缺失的
// 上层祖先为普通目录，丢弃上层 path 之下的全部记录并令 U(path) 为
// 白障；否则只丢弃上层 path 及其之下的全部记录，不补祖先。
func (fs *FS) Remove(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !validOpPath(path, false) {
		return ErrInvalidPath
	}
	if err := fs.checkAncestors(path); err != nil {
		return err
	}
	e, ok := fs.lookup(path)
	if !ok {
		return ErrNotFound
	}
	if e.Type == EntryDir && len(fs.readDirLocked(path)) > 0 {
		return ErrNotEmpty
	}
	if fs.lowerReachable(path) {
		fs.ensureAncestors(path)
		fs.dropSubtree(path)
		fs.upper[path] = record{kind: recWhiteout}
	} else {
		fs.dropSubtree(path)
	}
	return nil
}
