package overlay

import "sort"

// lowerReachable 报告 q 是否下层可达：L(q) 存在且 q 的每个真祖先
// 在上层都不是不透明目录。调用方须持有锁。
func (fs *FS) lowerReachable(q string) bool {
	if _, ok := fs.lower[q]; !ok {
		return false
	}
	for _, a := range ancestors(q) {
		if r, ok := fs.upper[a]; ok && r.kind == recOpaque {
			return false
		}
	}
	return true
}

// lookup 解析合并视图在 p 处的条目，p 为空串时解析根。
// 调用方须持有锁。
func (fs *FS) lookup(p string) (Entry, bool) {
	if p == "" {
		return Entry{Type: EntryDir}, true
	}
	if r, ok := fs.upper[p]; ok {
		switch r.kind {
		case recWhiteout:
			return Entry{}, false
		case recFile:
			return Entry{Type: EntryFile, Content: r.content}, true
		case recDir, recOpaque:
			return Entry{Type: EntryDir}, true
		}
	}
	if fs.lowerReachable(p) {
		e := fs.lower[p]
		if e.isDir {
			return Entry{Type: EntryDir}, true
		}
		return Entry{Type: EntryFile, Content: e.content}, true
	}
	return Entry{}, false
}

// Lookup 返回 path 处的条目类型与文件内容。
func (fs *FS) Lookup(path string) (Entry, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if !validOpPath(path, true) {
		return Entry{}, ErrInvalidPath
	}
	if err := fs.checkAncestors(path); err != nil {
		return Entry{}, err
	}
	e, ok := fs.lookup(path)
	if !ok {
		return Entry{}, ErrNotFound
	}
	e.Content = append([]byte(nil), e.Content...)
	return e, nil
}

// ReadDir 返回 path 处目录的子项名，按字节序升序。
func (fs *FS) ReadDir(path string) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if !validOpPath(path, true) {
		return nil, ErrInvalidPath
	}
	if err := fs.checkAncestors(path); err != nil {
		return nil, err
	}
	e, ok := fs.lookup(path)
	if !ok {
		return nil, ErrNotFound
	}
	if e.Type != EntryDir {
		return nil, ErrNotDir
	}
	return fs.readDirLocked(path), nil
}

// readDirLocked 计算目录 p（空串为根）的合并子项名列表。
// 调用方须持有锁。
func (fs *FS) readDirLocked(p string) []string {
	set := make(map[string]struct{})
	prefix := ""
	if p != "" {
		prefix = p + "/"
	}
	for q, r := range fs.upper {
		if parent(q) == p && r.kind != recWhiteout {
			set[q[len(prefix):]] = struct{}{}
		}
	}
	opaque := false
	if p != "" {
		if r, ok := fs.upper[p]; ok && r.kind == recOpaque {
			opaque = true
		}
	}
	if !opaque && (p == "" || fs.lowerReachable(p)) {
		if p == "" || fs.lower[p].isDir {
			for q := range fs.lower {
				if parent(q) == p {
					// 上层有任何记录（含白障）则隐藏下层同名项。
					if _, covered := fs.upper[q]; !covered {
						set[q[len(prefix):]] = struct{}{}
					}
				}
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Upper 返回上层全部记录，按路径升序。
func (fs *FS) Upper() []Record {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	paths := make([]string, 0, len(fs.upper))
	for p := range fs.upper {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]Record, 0, len(paths))
	for _, p := range paths {
		r := fs.upper[p]
		rec := Record{Path: p}
		switch r.kind {
		case recFile:
			rec.Kind = "file"
			rec.Content = append([]byte(nil), r.content...)
		case recDir:
			rec.Kind = "dir"
		case recOpaque:
			rec.Kind = "opaque"
		case recWhiteout:
			rec.Kind = "whiteout"
		}
		out = append(out, rec)
	}
	return out
}

// checkAncestors 自根向下解析 p 的真祖先：某祖先不存在报
// ErrNotFound，某祖先是文件报 ErrNotDir。调用方须持有锁。
func (fs *FS) checkAncestors(p string) error {
	for _, a := range ancestors(p) {
		e, ok := fs.lookup(a)
		if !ok {
			return ErrNotFound
		}
		if e.Type != EntryDir {
			return ErrNotDir
		}
	}
	return nil
}
