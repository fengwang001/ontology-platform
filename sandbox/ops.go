package sandbox

// Mkdir creates a new empty directory. All parent components must exist and
// resolve to directories; the final name must not already exist.
func (t *Tree) Mkdir(path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mkdirLocked(path)
}

// CreateFile creates a new empty regular file.
func (t *Tree) CreateFile(path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.createFileLocked(path)
}

// Symlink creates a symbolic link at linkPath whose content is the raw
// target text. Targets beginning with "/" are rooted at the sandbox root;
// other targets are interpreted relative to the link's directory.
func (t *Tree) Symlink(target, linkPath string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.symlinkLocked(target, linkPath)
}

// Remove removes a file, symbolic link, or empty directory. Symbolic links
// are removed themselves and are never followed.
func (t *Tree) Remove(path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.removeLocked(path)
}

// Rename moves the entry at oldPath to newPath. The destination name must
// not exist, and a directory cannot be moved into itself or beneath one of
// its own descendants. All validation precedes any mutation, so a rejected
// rename leaves the tree untouched.
func (t *Tree) Rename(oldPath, newPath string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.renameLocked(oldPath, newPath)
}

// parentAndName resolves the parent directory of path and returns the final
// path component. The parent is resolved with links followed.
func (t *Tree) parentAndName(path string) (*Node, string, error) {
	if path == "" {
		return nil, "", ErrEmptyPath
	}
	segments := splitSegments(path)
	if len(segments) == 0 {
		return nil, "", ErrInvalidName
	}
	name := segments[len(segments)-1]
	if !validName(name) || name == ".." {
		return nil, "", ErrInvalidName
	}
	if len(segments) == 1 {
		return t.root, name, nil
	}
	parentPath := joinSegments(segments[:len(segments)-1])
	res, err := t.resolveLocked(parentPath, true)
	if err != nil {
		return nil, "", err
	}
	if !res.Node.IsDir() {
		return nil, "", ErrNotDirectory
	}
	return res.Node, name, nil
}

func (t *Tree) mkdirLocked(path string) error {
	parent, name, err := t.parentAndName(path)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return ErrExists
	}
	node := newDirectory(name)
	node.parent = parent
	parent.children[name] = node
	return nil
}

func (t *Tree) createFileLocked(path string) error {
	parent, name, err := t.parentAndName(path)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return ErrExists
	}
	node := newFile(name)
	node.parent = parent
	parent.children[name] = node
	return nil
}

func (t *Tree) symlinkLocked(target, linkPath string) error {
	if target == "" {
		return ErrInvalidArgument
	}
	parent, name, err := t.parentAndName(linkPath)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return ErrExists
	}
	node := newSymlink(name, target)
	node.parent = parent
	parent.children[name] = node
	return nil
}

func (t *Tree) removeLocked(path string) error {
	parent, name, err := t.parentAndName(path)
	if err != nil {
		return err
	}
	node, ok := parent.children[name]
	if !ok {
		return ErrNotExist
	}
	if node.IsDir() && len(node.children) > 0 {
		return ErrDirectoryNotEmpty
	}
	delete(parent.children, name)
	node.parent = nil
	return nil
}

func (t *Tree) renameLocked(oldPath, newPath string) error {
	srcParent, srcName, err := t.parentAndName(oldPath)
	if err != nil {
		return err
	}
	dstParent, dstName, err := t.parentAndName(newPath)
	if err != nil {
		return err
	}
	src, ok := srcParent.children[srcName]
	if !ok {
		return ErrNotExist
	}
	if src.IsDir() {
		// Reject moving a directory onto itself or anywhere beneath it.
		if srcParent == dstParent && srcName == dstName {
			return ErrRenameIntoSelf
		}
		for cur := dstParent; cur != nil; cur = cur.parent {
			if cur == src {
				return ErrRenameIntoSelf
			}
		}
	}
	if srcParent == dstParent && srcName == dstName {
		return nil
	}
	if _, ok := dstParent.children[dstName]; ok {
		return ErrExists
	}

	delete(srcParent.children, srcName)
	src.name = dstName
	src.parent = dstParent
	dstParent.children[dstName] = src
	return nil
}

func joinSegments(segments []string) string {
	if len(segments) == 0 {
		return "/"
	}
	out := ""
	for _, seg := range segments {
		out += "/" + seg
	}
	return out
}
