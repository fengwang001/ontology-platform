package fsmapper

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var trailingNumberSuffix = regexp.MustCompile(`~[0-9]+$`)

// 哨兵错误。
var (
	// ErrInvalidName：src 为空、含 '/' 或 NUL、或不是合法 UTF-8。
	ErrInvalidName = errors.New("fsmapper: invalid name")
	// ErrNoParent：parent 不存在或不是目录。
	ErrNoParent = errors.New("fsmapper: no such parent directory")
	// ErrExists：同一目录下已存在同名（大小写敏感）条目。
	ErrExists = errors.New("fsmapper: entry already exists")
	// ErrNotFound：条目不存在。
	ErrNotFound = errors.New("fsmapper: entry not found")
	// ErrNotEmpty：删除非空目录。
	ErrNotEmpty = errors.New("fsmapper: directory not empty")
	// ErrFull：目录条目数达到 MaxEntries。
	ErrFull = errors.New("fsmapper: directory is full")
	// ErrCannotFit：冲突后缀候选无法压进 MaxBytes。
	ErrCannotFit = errors.New("fsmapper: name cannot fit")
	// ErrPathTooLong：映射后路径长度超过 MaxPath。
	ErrPathTooLong = errors.New("fsmapper: mapped path too long")
	// ErrInvalidConfig：构造参数超出允许范围。
	ErrInvalidConfig = errors.New("fsmapper: invalid configuration")
)

type entry struct {
	id       int64
	parent   int64
	src      string // 源端名字（大小写敏感）
	mapped   string // 映射名
	isDir    bool
	children []int64
	pathLen  int // 映射路径字节数（根为 0）
}

type dirState struct {
	bySrc map[string]int64 // 源名（大小写敏感）-> id
	byKey map[string]int64 // 映射名折叠键 -> id
}

func newDirState() *dirState {
	return &dirState{
		bySrc: map[string]int64{},
		byKey: map[string]int64{},
	}
}

// Mapper 把源端文件名映射为目标端安全文件名。
// 所有方法可并发调用，语义等价于某种串行顺序。
type Mapper struct {
	mu sync.RWMutex

	maxBytes   int
	maxPath    int
	maxEntries int

	nextID  int64
	entries map[int64]*entry
	dirs    map[int64]*dirState
}

// New 创建 Mapper。
// maxBytes 为单个映射名字节上限（16..255），maxPath 为整条映射路径
// 字节上限（maxBytes..4096），maxEntries 为每个目录条目数上限（1..1e5）。
func New(maxBytes, maxPath, maxEntries int) (*Mapper, error) {
	if maxBytes < 16 || maxBytes > 255 {
		return nil, ErrInvalidConfig
	}
	if maxPath < maxBytes || maxPath > 4096 {
		return nil, ErrInvalidConfig
	}
	if maxEntries < 1 || maxEntries > 100000 {
		return nil, ErrInvalidConfig
	}
	m := &Mapper{
		maxBytes:   maxBytes,
		maxPath:    maxPath,
		maxEntries: maxEntries,
		nextID:     1,
		entries:    map[int64]*entry{},
		dirs:       map[int64]*dirState{},
	}
	root := &entry{id: 0, isDir: true}
	m.entries[0] = root
	m.dirs[0] = newDirState()
	return m, nil
}

// Add 在 parent 目录下新增条目，返回其 id。
func (m *Mapper) Add(parent int64, src string, isDir bool) (int64, error) {
	if !validSrcName(src) {
		return 0, ErrInvalidName
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	dir, ok := m.resolveDir(parent)
	if !ok {
		return 0, ErrNoParent
	}
	if _, ok := dir.bySrc[src]; ok {
		return 0, ErrExists
	}
	if len(dir.bySrc) >= m.maxEntries {
		return 0, ErrFull
	}
	mapped, err := m.allocate(dir, src)
	if err != nil {
		return 0, err
	}

	pEntry := m.entries[parent]
	e := &entry{
		id:      m.nextID,
		parent:  parent,
		src:     src,
		mapped:  mapped,
		isDir:   isDir,
		pathLen: pEntry.pathLen + 1 + len(mapped),
	}

	// 路径长度判定在分配之后；失败需回滚全部变更。
	// allocate 不提交任何键占用，所以此处无需撤销目录状态。
	if e.pathLen > m.maxPath {
		return 0, ErrPathTooLong
	}

	m.nextID++
	m.entries[e.id] = e
	pEntry.children = append(pEntry.children, e.id)
	dir.bySrc[src] = e.id
	dir.byKey[asciiFold(mapped)] = e.id
	if isDir {
		m.dirs[e.id] = newDirState()
	}
	return e.id, nil
}

// Remove 删除 parent 目录下名为 src（大小写敏感）的条目。
func (m *Mapper) Remove(parent int64, src string) error {
	if !validSrcName(src) {
		return ErrInvalidName
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	dir, ok := m.resolveDir(parent)
	if !ok {
		return ErrNoParent
	}
	id, ok := dir.bySrc[src]
	if !ok {
		return ErrNotFound
	}
	ent := m.entries[id]
	if ent.isDir && len(ent.children) > 0 {
		return ErrNotEmpty
	}

	pEntry := m.entries[parent]
	pEntry.children = removeID(pEntry.children, id)
	delete(dir.bySrc, src)
	delete(dir.byKey, asciiFold(ent.mapped))
	delete(m.entries, id)
	if ent.isDir {
		delete(m.dirs, id)
	}
	return nil
}

// Rename 在同一目录内改名，条目 id 不变。
func (m *Mapper) Rename(parent int64, src, newSrc string) error {
	if !validSrcName(newSrc) {
		return ErrInvalidName
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	dir, ok := m.resolveDir(parent)
	if !ok {
		return ErrNoParent
	}
	id, ok := dir.bySrc[src]
	if !ok {
		return ErrNotFound
	}
	if newSrc == src {
		return nil
	}
	if _, ok := dir.bySrc[newSrc]; ok {
		return ErrExists
	}
	ent := m.entries[id]

	// 先释放旧名字，再按 Add 规则为新名字分配（保证释放→分配次序）。
	oldMapped := ent.mapped
	delete(dir.bySrc, src)
	delete(dir.byKey, asciiFold(oldMapped))

	newMapped, err := m.allocate(dir, newSrc)
	if err != nil {
		// 恢复调用前状态。
		dir.bySrc[src] = id
		dir.byKey[asciiFold(oldMapped)] = id
		return err
	}

	// 对整棵子树施加路径长度增量并判定，任一超长则整体回滚。
	delta := len(newMapped) - len(oldMapped)
	if !m.subtreeFits(id, delta) {
		// allocate 未提交键；恢复释放掉的旧名字。
		dir.bySrc[src] = id
		dir.byKey[asciiFold(oldMapped)] = id
		return ErrPathTooLong
	}
	m.bumpSubtree(id, delta)

	ent.src = newSrc
	ent.mapped = newMapped
	dir.bySrc[newSrc] = id
	dir.byKey[asciiFold(newMapped)] = id
	return nil
}

// Lookup 返回 parent 目录下 src（大小写敏感）对应条目的 id 与 isDir。
func (m *Mapper) Lookup(parent int64, src string) (id int64, isDir bool, err error) {
	if !validSrcName(src) {
		return 0, false, ErrInvalidName
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	dir, ok := m.resolveDir(parent)
	if !ok {
		return 0, false, ErrNoParent
	}
	e, ok := dir.bySrc[src]
	if !ok {
		return 0, false, ErrNotFound
	}
	return e, m.entries[e].isDir, nil
}

// Path 返回 id 的映射路径（根返回空串）。
func (m *Mapper) Path(id int64) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.entries[id]; !ok {
		return "", ErrNotFound
	}
	var parts []string
	for cur := id; cur != 0; {
		ent := m.entries[cur]
		parts = append(parts, ent.mapped)
		cur = ent.parent
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/"), nil
}

// Names 返回 parent 目录下全部映射名，按字节序排序。
func (m *Mapper) Names(parent int64) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dir, ok := m.resolveDir(parent)
	if !ok {
		return nil, ErrNoParent
	}
	out := make([]string, 0, len(dir.bySrc))
	for _, id := range dir.bySrc {
		out = append(out, m.entries[id].mapped)
	}
	sort.Strings(out)
	return out, nil
}

// resolveDir 调用时持锁。
func (m *Mapper) resolveDir(id int64) (*dirState, bool) {
	ent, ok := m.entries[id]
	if !ok || !ent.isDir {
		return nil, false
	}
	return m.dirs[id], true
}

// allocate 计算基础名并在目录内消解冲突，但不提交条目状态。
// 调用时 src 已合法且目录下不存在该源名。
func (m *Mapper) allocate(dir *dirState, src string) (string, error) {
	t0 := baseMap(src, m.maxBytes)
	if _, taken := dir.byKey[asciiFold(t0)]; !taken {
		return t0, nil
	}
	base, ext := splitBaseExt(t0)
	// 编号前剥掉主干末尾已有的一个 "~<数字>"，使 "readme~2.MD"
	// 在 ~2、~3 键均被占用时直接得到 readme~4.MD。
	base = trailingNumberSuffix.ReplaceAllString(base, "")
	for n := 2; ; n++ {
		suffix := "~" + strconv.Itoa(n)
		limit := m.maxBytes - len(suffix) - len(ext)
		if limit < 0 {
			return "", ErrCannotFit
		}
		b, ok := shortenBaseTo(base, limit)
		if !ok {
			return "", ErrCannotFit
		}
		cand := b + suffix + ext
		if _, taken := dir.byKey[asciiFold(cand)]; !taken {
			return cand, nil
		}
	}
}

// subtreeFits 报告 root 及其全部后代 pathLen 加 delta 后是否仍不超 maxPath。
func (m *Mapper) subtreeFits(root int64, delta int) bool {
	stack := []int64{root}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if m.entries[cur].pathLen+delta > m.maxPath {
			return false
		}
		stack = append(stack, m.entries[cur].children...)
	}
	return true
}

// bumpSubtree 给 root 及其全部后代 pathLen 加 delta。
func (m *Mapper) bumpSubtree(root int64, delta int) {
	stack := []int64{root}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		m.entries[cur].pathLen += delta
		stack = append(stack, m.entries[cur].children...)
	}
}

func removeID(ids []int64, target int64) []int64 {
	for i, v := range ids {
		if v == target {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}
