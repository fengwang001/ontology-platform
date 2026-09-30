// Package vfs 实现一个带符号链接的内存文件树及其沙箱路径解析器。
//
// 所有路径均以沙箱根为起点：以斜杠开头的路径与符号链接目标都从根解析。
// 解析按物理语义逐段进行：空段与 "." 忽略，".." 回到当前真实所在目录的
// 父目录（经过符号链接后回到链接目标的父目录），根处的 ".." 停在根。
// 任何解析结果都不会越出根目录。
//
// 并发安全：解析与修改通过全局读写锁串行化，每次解析的结果总等于
// 某一时刻整棵树上的串行解析结果。
package vfs

import (
	"errors"
	"strings"
	"sync"
)

// MaxFollows 是单次解析允许累计跟随符号链接的最大次数，超过即判为循环。
const MaxFollows = 40

// 可区分的拒绝原因。
var (
	ErrEmptyPath    = errors.New("vfs: 路径为空")
	ErrNotExist     = errors.New("vfs: 段不存在")
	ErrNotDir       = errors.New("vfs: 中间段不是目录")
	ErrTooManyLinks = errors.New("vfs: 符号链接跟随超限（循环）")
	ErrInvalidName  = errors.New("vfs: 名字为空或含斜杠")
	ErrExist        = errors.New("vfs: 名字已存在")
	ErrDirNotEmpty  = errors.New("vfs: 删除非空目录")
	ErrMoveIntoSelf = errors.New("vfs: 不能把目录移到其自身或子孙之下")
	ErrRoot         = errors.New("vfs: 不能删除或移动根目录")
)

// Kind 是节点类型：目录、文件或符号链接。
type Kind int

const (
	Dir Kind = iota
	File
	Symlink
)

func (k Kind) String() string {
	switch k {
	case Dir:
		return "dir"
	case File:
		return "file"
	case Symlink:
		return "symlink"
	}
	return "unknown"
}

// Entry 是一次解析结果的快照，与树解耦，可安全跨锁使用。
type Entry struct {
	Kind   Kind   // 解析终点节点类型
	Path   string // 终点在沙箱内的规范绝对路径
	Target string // 仅当未跟随末段链接时，为链接目标文本
}

type node struct {
	kind     Kind
	name     string
	parent   *node // 根的 parent 指向自身
	children map[string]*node
	target   string // 仅符号链接有效
}

// Tree 是一棵并发安全的内存文件树。
type Tree struct {
	mu   sync.RWMutex
	root *node
}

// NewTree 创建一棵只有根目录的空树。
func NewTree() *Tree {
	root := &node{kind: Dir, name: "", children: make(map[string]*node)}
	root.parent = root
	return &Tree{root: root}
}

// segments 切分路径，丢弃空段与 "."，保留 ".." 与普通名字。
func segments(path string) []string {
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s == "" || s == "." {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Resolve 按物理语义逐段解析 path，返回终点快照。
//
// followLast 为 true 时末段若是符号链接也被跟随；中间段的链接总是跟随。
// 单次解析累计跟随超过 MaxFollows 次返回 ErrTooManyLinks。
func (t *Tree) Resolve(path string, followLast bool) (Entry, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n, err := t.resolveLocked(path, followLast)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Kind: n.kind, Path: pathOf(n), Target: n.target}, nil
}

// resolveLocked 在持锁状态下解析 path，返回终点节点。
func (t *Tree) resolveLocked(path string, followLast bool) (*node, error) {
	if path == "" {
		return nil, ErrEmptyPath
	}
	follows := 0
	cur := t.root
	segs := segments(path)
	for i, seg := range segs {
		last := i == len(segs)-1
		if seg == ".." {
			// 物理语义：回到当前真实所在目录的父目录；根的父目录是根自身。
			cur = cur.parent
			continue
		}
		if cur.kind != Dir {
			return nil, ErrNotDir
		}
		child, ok := cur.children[seg]
		if !ok {
			return nil, ErrNotExist
		}
		if child.kind == Symlink && (!last || followLast) {
			follows++
			if follows > MaxFollows {
				return nil, ErrTooManyLinks
			}
			var err error
			child, err = t.followLocked(child, &follows)
			if err != nil {
				return nil, err
			}
		}
		cur = child
	}
	return cur, nil
}

// followLocked 把符号链接解析到真实节点：绝对目标从根开始，
// 相对目标相对于链接所在目录；目标中的链接递归跟随，累计计入 follows。
func (t *Tree) followLocked(link *node, follows *int) (*node, error) {
	cur := link.parent
	if strings.HasPrefix(link.target, "/") {
		cur = t.root
	}
	for _, seg := range segments(link.target) {
		if seg == ".." {
			cur = cur.parent
			continue
		}
		if cur.kind != Dir {
			return nil, ErrNotDir
		}
		child, ok := cur.children[seg]
		if !ok {
			return nil, ErrNotExist
		}
		if child.kind == Symlink {
			*follows++
			if *follows > MaxFollows {
				return nil, ErrTooManyLinks
			}
			var err error
			child, err = t.followLocked(child, follows)
			if err != nil {
				return nil, err
			}
		}
		cur = child
	}
	return cur, nil
}

// pathOf 沿父指针回溯出节点的规范绝对路径，不会越出根。
func pathOf(n *node) string {
	var parts []string
	for n.parent != n {
		parts = append(parts, n.name)
		n = n.parent
	}
	var b strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteByte('/')
		b.WriteString(parts[i])
	}
	if b.Len() == 0 {
		return "/"
	}
	return b.String()
}

// validName 校验节点名字：非空、不含斜杠，且不是 "." / ".."。
func validName(name string) error {
	if name == "" || strings.Contains(name, "/") || name == "." || name == ".." {
		return ErrInvalidName
	}
	return nil
}

// Mkdir 在 parentPath 指向的目录下创建名为 name 的目录。
func (t *Tree) Mkdir(parentPath, name string) error {
	return t.create(parentPath, name, Dir, "")
}

// CreateFile 在 parentPath 指向的目录下创建名为 name 的文件。
func (t *Tree) CreateFile(parentPath, name string) error {
	return t.create(parentPath, name, File, "")
}

// Symlink 在 parentPath 指向的目录下创建名为 name、目标为 target 的符号链接。
// target 为绝对或相对路径文本，创建时不校验目标是否存在。
func (t *Tree) Symlink(parentPath, name, target string) error {
	return t.create(parentPath, name, Symlink, target)
}

// create 是三种创建的公共实现；任何校验失败都不改变树。
func (t *Tree) create(parentPath, name string, kind Kind, target string) error {
	if err := validName(name); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	parent, err := t.resolveLocked(parentPath, true)
	if err != nil {
		return err
	}
	if parent.kind != Dir {
		return ErrNotDir
	}
	if _, ok := parent.children[name]; ok {
		return ErrExist
	}
	n := &node{kind: kind, name: name, parent: parent, target: target}
	if kind == Dir {
		n.children = make(map[string]*node)
	}
	parent.children[name] = n
	return nil
}

// Remove 删除 path 指向的节点；末段链接不被跟随（删除链接自身）。
// 非空目录与根目录拒绝删除；失败时不改变树。
func (t *Tree) Remove(path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.resolveLocked(path, false)
	if err != nil {
		return err
	}
	if n == t.root {
		return ErrRoot
	}
	if n.kind == Dir && len(n.children) > 0 {
		return ErrDirNotEmpty
	}
	delete(n.parent.children, n.name)
	return nil
}

// Rename 把 srcPath 指向的节点移动到 dstDirPath 目录下并改名为 newName。
// 目录不得移入其自身或子孙之下；目标名字已存在时拒绝；失败时不改变树。
func (t *Tree) Rename(srcPath, dstDirPath, newName string) error {
	if err := validName(newName); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	src, err := t.resolveLocked(srcPath, false)
	if err != nil {
		return err
	}
	if src == t.root {
		return ErrRoot
	}
	dstDir, err := t.resolveLocked(dstDirPath, true)
	if err != nil {
		return err
	}
	if dstDir.kind != Dir {
		return ErrNotDir
	}
	if src.kind == Dir {
		for p := dstDir; ; {
			if p == src {
				return ErrMoveIntoSelf
			}
			if p.parent == p {
				break
			}
			p = p.parent
		}
	}
	if existing, ok := dstDir.children[newName]; ok {
		if existing == src {
			return nil // 原地同名，视为无操作
		}
		return ErrExist
	}
	delete(src.parent.children, src.name)
	src.parent = dstDir
	src.name = newName
	dstDir.children[newName] = src
	return nil
}
